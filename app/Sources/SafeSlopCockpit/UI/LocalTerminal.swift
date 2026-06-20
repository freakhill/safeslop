import SwiftUI
import SwiftTerm

/// LocalTerminal embeds SwiftTerm's `LocalProcessTerminalView` — a real terminal on a local PTY —
/// and runs `argv` (here: `safeslop run <profile>`, which itself does the trust gate + sandbox /
/// container / vm + env scrub + creds + ctty). So the widget handles ALL terminal behavior natively
/// (control keys, modes, resize, scrollback, process exit) and the engine still does the isolation —
/// no hand-rolled PTY-over-gRPC bridge (specs/research/2026-06-20-cockpit-safe-by-design.md).
/// `onExit` fires when the process terminates, so the window can close itself.
struct LocalTerminal: NSViewRepresentable {
    let argv: [String]
    let onExit: @MainActor (Int32?) -> Void

    func makeNSView(context: Context) -> LocalProcessTerminalView {
        let tv = LocalProcessTerminalView(frame: .zero)
        tv.processDelegate = context.coordinator
        // Inherit the app's environment (PATH must contain `safeslop`); SwiftTerm wants KEY=VALUE.
        let env = ProcessInfo.processInfo.environment.map { "\($0.key)=\($0.value)" }
        tv.startProcess(executable: argv[0], args: Array(argv.dropFirst()), environment: env)
        DispatchQueue.main.async { [weak tv] in tv?.window?.makeFirstResponder(tv) }
        return tv
    }

    func updateNSView(_ nsView: LocalProcessTerminalView, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(onExit: onExit) }

    final class Coordinator: NSObject, LocalProcessTerminalViewDelegate {
        let onExit: @MainActor (Int32?) -> Void
        init(onExit: @escaping @MainActor (Int32?) -> Void) { self.onExit = onExit }

        func processTerminated(source: TerminalView, exitCode: Int32?) {
            let cb = onExit
            Task { @MainActor in cb(exitCode) }
        }
        func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}
        func setTerminalTitle(source: LocalProcessTerminalView, title: String) {}
        func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    }
}
