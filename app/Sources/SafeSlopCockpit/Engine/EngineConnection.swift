import Foundation
import GRPCCore
import GRPCNIOTransportHTTP2

// EngineConnection centralizes how the app reaches the safeslop engine: the UDS socket path,
// building a plaintext HTTP/2-over-UDS transport, and launch-on-demand of `safeslop serve`
// (specs/0014 §10 — the app starts the engine if Ping fails, then retries).
enum EngineConnection {
    /// ~/.safeslop/s.sock — the engine's control-plane socket (see internal/engine/control/serve.go).
    static var socketPath: String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return home + "/.safeslop/s.sock"
    }

    /// A plaintext HTTP/2 transport dialing the engine's Unix-domain socket.
    static func makeTransport() throws -> HTTP2ClientTransport.Posix {
        try HTTP2ClientTransport.Posix(
            target: .unixDomainSocket(path: socketPath),
            transportSecurity: .plaintext
        )
    }

    /// Returns the engine version if reachable, else nil. Used to decide whether to launch-on-demand.
    static func ping() async -> String? {
        do {
            let transport = try makeTransport()
            return try await withGRPCClient(transport: transport) { client in
                let control = Safeslop_Control_V1_Control.Client(wrapping: client)
                let resp = try await control.ping(.init())
                return resp.version
            }
        } catch {
            return nil
        }
    }

    /// Lists the profiles declared in the engine's safeslop.cue (configPath empty => engine cwd).
    static func listProfiles(configPath: String = "") async throws -> [Safeslop_Control_V1_Profile] {
        let transport = try makeTransport()
        return try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            let resp = try await control.listProfiles(.with { $0.configPath = configPath })
            return resp.profiles
        }
    }

    /// Records host-side approval of the safeslop.cue at configPath (the Trust RPC) so a subsequent
    /// OpenSession passes the engine's fail-closed trust gate (specs/0024 S1a). Returns the approved
    /// absolute path. configPath empty => the engine resolves it from its cwd (same as OpenSession).
    @discardableResult
    static func trust(configPath: String = "") async throws -> String {
        let transport = try makeTransport()
        return try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            let resp = try await control.trust(.with { $0.configPath = configPath })
            return resp.trustedPath
        }
    }

    /// The pinned desired-state install plan (the SP7b-2 diff) for the Installs tab to preview.
    static func installPlan() async throws -> [Safeslop_Control_V1_InstallAction] {
        let transport = try makeTransport()
        return try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            let resp = try await control.installPlan(.init())
            return resp.actions
        }
    }

    /// Applies the install plan, streaming progress events (download, verify fail-closed, install).
    /// `onEvent` is called on the engine's stream order; it runs off the main actor, so the caller
    /// hops to @MainActor to mutate UI state.
    static func installApply(onEvent: @Sendable @escaping (Safeslop_Control_V1_InstallApplyEvent) async -> Void) async throws {
        let transport = try makeTransport()
        try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            try await control.installApply(.init()) { response in
                for try await event in response.messages {
                    await onEvent(event)
                }
            }
        }
    }

    /// The Installs-tab tool catalog. catalogOnly=true returns instantly with detection deferred
    /// (every tool Source "unknown") for a first paint; false runs the real (brew-backed) detection.
    static func listTools(catalogOnly: Bool = false) async throws -> [Safeslop_Control_V1_ToolStatus] {
        let transport = try makeTransport()
        return try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            let resp = try await control.listTools(.with { $0.catalogOnly = catalogOnly })
            return resp.tools
        }
    }

    /// Installs ONE missing tool by name, streaming output lines. The engine refuses present tools.
    static func installTool(name: String, onLine: @Sendable @escaping (String) async -> Void) async throws {
        let transport = try makeTransport()
        try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            try await control.installTool(.with { $0.name = name }) { response in
                for try await event in response.messages {
                    if !event.line.isEmpty { await onLine(event.line) }
                }
            }
        }
    }

    /// Validates unsaved safeslop.cue text from the editor, returning a cue-vet error or the parsed
    /// profiles tagged with their tier + arbiter risk (the Create tab's live feedback loop).
    static func validatePolicy(_ cueText: String) async throws -> Safeslop_Control_V1_ValidatePolicyResponse {
        let transport = try makeTransport()
        return try await withGRPCClient(transport: transport) { client in
            let control = Safeslop_Control_V1_Control.Client(wrapping: client)
            return try await control.validatePolicy(.with { $0.cueText = cueText })
        }
    }

    /// Ensures `safeslop serve` is running: pings, and if unreachable spawns the binary and
    /// polls until the socket answers (or a timeout). `safeslop` is expected on PATH.
    @discardableResult
    static func ensureServing(timeout: Duration = .seconds(10)) async -> Bool {
        if await ping() != nil { return true }
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        proc.arguments = ["safeslop", "serve"]
        do { try proc.run() } catch { return false }

        let deadline = ContinuousClock.now.advanced(by: timeout)
        while ContinuousClock.now < deadline {
            if await ping() != nil { return true }
            try? await Task.sleep(for: .milliseconds(200))
        }
        return false
    }
}
