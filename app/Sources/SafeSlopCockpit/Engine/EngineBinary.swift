import Foundation

/// EngineBinary resolves the `safeslop` engine to run. A distributed .app ships the engine inside it
/// (Contents/MacOS/safeslop), so a double-clicked app works with no PATH setup; in dev (`swift run`,
/// no bundle) it falls back to `safeslop` on PATH. Returned as (executable, prefixArgs) so callers
/// append their verb — e.g. resolved.prefixArgs + ["serve"] or + ["run", name].
enum EngineBinary {
    static var resolved: (executable: String, prefixArgs: [String]) {
        let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/safeslop").path
        if FileManager.default.isExecutableFile(atPath: bundled) {
            return (bundled, [])
        }
        return ("/usr/bin/env", ["safeslop"]) // dev / unbundled: resolve on PATH
    }
}
