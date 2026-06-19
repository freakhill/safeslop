import SwiftUI
import SwiftTerm

/// TerminalBridge embeds a SwiftTerm `TerminalView` (an AppKit NSView) in SwiftUI and wires it to
/// a CockpitSession: keystrokes + resizes flow out to the engine PTY; PTY output is fed back in.
struct TerminalBridge: NSViewRepresentable {
    let session: CockpitSession

    func makeNSView(context: Context) -> TerminalView {
        let tv = TerminalView(frame: .zero)
        tv.terminalDelegate = context.coordinator
        context.coordinator.terminalView = tv
        // PTY output (from the Attach stream) is fed straight into the emulator on the main actor.
        session.onOutput = { [weak tv] bytes in tv?.feed(byteArray: bytes[...]) }
        session.start()
        return tv
    }

    func updateNSView(_ nsView: TerminalView, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(session: session) }

    final class Coordinator: NSObject, TerminalViewDelegate {
        let session: CockpitSession
        weak var terminalView: TerminalView?
        init(session: CockpitSession) { self.session = session }

        // Bytes typed by the user -> the agent's PTY.
        func send(source: TerminalView, data: ArraySlice<UInt8>) {
            session.write(Array(data))
        }

        // Window/font resize -> propagate to the agent's PTY (engine calls pty.Setsize).
        func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
            session.resize(cols: UInt32(max(0, newCols)), rows: UInt32(max(0, newRows)))
        }

        // Remaining TerminalViewDelegate hooks are not needed for the cockpit's data plane.
        func setTerminalTitle(source: TerminalView, title: String) {}
        func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
        func scrolled(source: TerminalView, position: Double) {}
        func requestOpenLink(source: TerminalView, link: String, params: [String: String]) {}
        func bell(source: TerminalView) {}
        func clipboardCopy(source: TerminalView, content: Data) {
            if let s = String(data: content, encoding: .utf8) {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(s, forType: .string)
            }
        }
        func rangeChanged(source: TerminalView, startY: Int, endY: Int) {}
    }
}
