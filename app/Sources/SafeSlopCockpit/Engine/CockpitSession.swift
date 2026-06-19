import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2
import Observation

/// CockpitSession owns one engine session end-to-end: it opens the session, holds the long-lived
/// Attach bidi stream, pumps PTY output to the terminal, and forwards typed input + resizes back.
/// One session per cockpit window (specs/0014 §4).
@MainActor
@Observable
final class CockpitSession {
    enum State: Equatable { case opening, needsTrust(String), running, closed, error(String) }

    let profile: Safeslop_Control_V1_Profile
    let configPath: String
    private(set) var state: State = .opening
    private(set) var sessionID: String = ""
    private(set) var exitCode: Int? = nil

    /// Set by the terminal bridge: each chunk of PTY output is delivered here on the main actor.
    var onOutput: (@MainActor ([UInt8]) -> Void)?

    // Outbound client frames (input + resize) are pushed onto this stream; the Attach producer drains it.
    private let outbound: AsyncStream<Safeslop_Control_V1_ClientFrame>
    private let outboundContinuation: AsyncStream<Safeslop_Control_V1_ClientFrame>.Continuation
    private var task: Task<Void, Never>?

    init(profile: Safeslop_Control_V1_Profile, configPath: String = "") {
        self.profile = profile
        self.configPath = configPath
        (self.outbound, self.outboundContinuation) =
            AsyncStream<Safeslop_Control_V1_ClientFrame>.makeStream()
    }

    /// Opens the session and runs the Attach stream until the agent exits or close() is called.
    func start(cols: UInt32 = 80, rows: UInt32 = 24) {
        guard task == nil else { return }
        task = Task { await run(cols: cols, rows: rows) }
    }

    /// Forward bytes typed in the terminal to the agent's PTY. `nonisolated` so SwiftTerm's
    /// (nonisolated) delegate can call it directly — it only yields to a Sendable continuation.
    nonisolated func write(_ bytes: [UInt8]) {
        outboundContinuation.yield(.with { $0.input = Data(bytes) })
    }

    /// Forward a terminal resize to the agent's PTY (engine applies it via pty.Setsize).
    nonisolated func resize(cols: UInt32, rows: UInt32) {
        outboundContinuation.yield(.with { $0.resize = .with { r in r.cols = cols; r.rows = rows } })
    }

    /// Close the window's session: tell the engine to tear it down, then stop the stream.
    func close() {
        let id = sessionID
        outboundContinuation.finish()
        task?.cancel()
        Task {
            guard !id.isEmpty else { return }
            let transport = try? EngineConnection.makeTransport()
            guard let transport else { return }
            _ = try? await withGRPCClient(transport: transport) { client in
                let control = Safeslop_Control_V1_Control.Client(wrapping: client)
                _ = try await control.closeSession(.with { $0.sessionID = id })
            }
        }
        state = .closed
    }

    private func run(cols: UInt32, rows: UInt32) async {
        do {
            let transport = try EngineConnection.makeTransport()
            try await withGRPCClient(transport: transport) { client in
                let control = Safeslop_Control_V1_Control.Client(wrapping: client)

                let open = try await control.openSession(.with {
                    $0.profile = self.profile.name
                    $0.cols = cols
                    $0.rows = rows
                })
                await MainActor.run {
                    self.sessionID = open.sessionID
                    self.state = .running
                }

                let attachID = open.sessionID
                let request = StreamingClientRequest<Safeslop_Control_V1_ClientFrame> { writer in
                    // The first frame MUST carry attach_session_id (control.proto §ClientFrame).
                    try await writer.write(.with { $0.attachSessionID = attachID })
                    for await frame in self.outbound {
                        try await writer.write(frame)
                    }
                }

                try await control.attach(request: request) { response in
                    for try await frame in response.messages {
                        switch frame.msg {
                        case .output(let bytes):
                            let arr = [UInt8](bytes)
                            await MainActor.run { self.onOutput?(arr) }
                        case .exited(let ex):
                            await MainActor.run {
                                self.exitCode = Int(ex.exitCode)
                                self.state = .closed
                            }
                        case .none:
                            break
                        }
                    }
                }
            }
        } catch is CancellationError {
            // close() was called — normal teardown.
        } catch {
            // The engine fail-closes OpenSession on an untrusted/changed safeslop.cue (specs/0024
            // S1a). Surface that as needsTrust (the in-place trust sheet), not a generic error.
            let desc = String(describing: error)
            await MainActor.run {
                if desc.contains("not trusted") || desc.contains("changed since you trusted") {
                    self.state = .needsTrust(desc)
                } else {
                    self.state = .error(desc)
                }
            }
        }
    }

    /// Approve the repo's safeslop.cue via the Trust RPC, then restart the session — called from the
    /// trust sheet after the user reviews the profile's capabilities (the safe-by-design trust flow,
    /// specs/research/2026-06-20-cockpit-safe-by-design.md). The engine's peer-auth (uid +
    /// process-tree) already gates who may approve.
    func approveTrustAndRetry(cols: UInt32 = 80, rows: UInt32 = 24) {
        state = .opening
        task?.cancel()
        task = nil
        Task {
            do {
                _ = try await EngineConnection.trust(configPath: configPath)
            } catch {
                await MainActor.run { self.state = .error("trust failed: \(String(describing: error))") }
                return
            }
            await MainActor.run { self.start(cols: cols, rows: rows) }
        }
    }
}
