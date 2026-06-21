import SwiftUI

/// One comprehension row in the host-launch gate (specs/0030), mirroring the engine's
/// Safeslop_Control_V1_ConsentStatement. `expected` is the engine's ground truth (is this statement TRUE
/// of this host run); `answer` is what the user has selected so far (nil = not yet answered, so a stray
/// default can't pre-arm the launch). The view never judges correctness itself — it compares `answer`
/// to the engine-authored `expected`.
struct ConsentStatement: Identifiable, Equatable {
    let id = UUID()
    let text: String
    let expected: Bool
    let tierOrigin: String
    var answer: Bool? = nil

    init(text: String, expected: Bool, tierOrigin: String, answer: Bool? = nil) {
        self.text = text; self.expected = expected; self.tierOrigin = tierOrigin; self.answer = answer
    }
    init(_ p: Safeslop_Control_V1_ConsentStatement) {
        self.init(text: p.text, expected: p.expected, tierOrigin: p.tierOrigin)
    }
}

/// Preflight is the engine-authored host-launch gate payload, mirroring
/// Safeslop_Control_V1_PreflightHostLaunchResponse: the fixed honesty headline, the per-launch live
/// scope line, and the shuffled consent rows.
struct Preflight: Equatable {
    var headlineBody: String
    var scopeLine: String
    var statements: [ConsentStatement]

    init(headlineBody: String, scopeLine: String, statements: [ConsentStatement]) {
        self.headlineBody = headlineBody; self.scopeLine = scopeLine; self.statements = statements
    }
    init(_ p: Safeslop_Control_V1_PreflightHostLaunchResponse) {
        self.init(headlineBody: p.headlineBody, scopeLine: p.scopeLine,
                  statements: p.statements.map(ConsentStatement.init))
    }
}

/// HostConsentModel is the pure arming state machine behind the host-launch comprehension gate. It holds
/// the engine-authored statements and the user's in-progress answers, exposes `allMatched` (every row's
/// answer equals the engine's `expected`), and — the anti-reflex rule — disarms and clears every answer
/// the instant a wrong toggle lands, flagging the offending row so the user must re-read. It does NOT
/// talk to the engine or invoke Touch ID; the view wires those around it, which keeps it unit-testable.
/// (Per-launch reshuffle is the engine's job: each phase entry calls PreflightHostLaunch and re-draws.)
@Observable
@MainActor
final class HostConsentModel {
    private(set) var statements: [ConsentStatement]
    let headlineBody: String
    let scopeLine: String
    /// The id of the row the user just answered wrong (cleared on the next correct answer); drives the red flag.
    private(set) var wrongRowID: UUID?

    init(_ preflight: Preflight) {
        self.statements = preflight.statements
        self.headlineBody = preflight.headlineBody
        self.scopeLine = preflight.scopeLine
    }

    /// Launch is armed only when every row is answered AND each answer matches the engine's ground truth.
    var allMatched: Bool {
        !statements.isEmpty && statements.allSatisfy { $0.answer == $0.expected }
    }

    /// Record the user's No/Yes for one row. A correct answer sticks; a WRONG answer immediately disarms
    /// the whole gate — clears every answer and flags the bad row — so a reflex click can't accumulate a
    /// correct set by luck. Comprehension must be demonstrated in one clean pass.
    func answer(_ id: UUID, _ value: Bool) {
        guard let i = statements.firstIndex(where: { $0.id == id }) else { return }
        if value == statements[i].expected {
            statements[i].answer = value
            wrongRowID = nil
        } else {
            for j in statements.indices { statements[j].answer = nil }
            wrongRowID = id
        }
    }
}
