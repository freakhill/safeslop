import Foundation
import LocalAuthentication

/// BiometricGate is native proof-of-intent at a privilege boundary (specs/research/
/// 2026-06-20-cockpit-safe-by-design.md): TouchID/biometric, falling back to the login password.
/// Used **sparingly** — only host-tier launch and re-approving an *edited* policy — so it stays a
/// meaningful signal and never becomes biometric habituation. Never gate opening a sandbox
/// session, switching profiles, or attaching.
///
/// Note: full biometric fidelity needs a signed app with the keychain-access-groups entitlement;
/// unsigned `swift run` falls back to the device password. The system presents the prompt — no
/// custom UI.
enum BiometricGate {
    /// Returns true iff the user confirmed. `reason` is shown in the system prompt.
    static func confirm(reason: String) async -> Bool {
        let ctx = LAContext()
        ctx.localizedFallbackTitle = "Use Password"
        var err: NSError?
        // Prefer biometrics, but allow the device password so it works without TouchID hardware.
        let policy: LAPolicy = ctx.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &err)
            ? .deviceOwnerAuthenticationWithBiometrics
            : .deviceOwnerAuthentication
        return await withCheckedContinuation { cont in
            ctx.evaluatePolicy(policy, localizedReason: reason) { ok, _ in
                cont.resume(returning: ok)
            }
        }
    }
}
