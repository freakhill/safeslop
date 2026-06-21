import Testing
@testable import SafeSlopCockpit

/// Arming-logic tests for the host-launch comprehension gate (specs/0030). The model is pure — no
/// engine, no Touch ID — so these exercise just the No/Yes → arm/disarm state machine.
@MainActor
struct HostConsentModelTests {
    private func preflight() -> Preflight {
        Preflight(headlineBody: "runs as you, no isolation",
                  scopeLine: "home + full network",
                  statements: [
                    ConsentStatement(text: "reads every file", expected: true, tierOrigin: "host"),
                    ConsentStatement(text: "confined to workspace", expected: false, tierOrigin: "sandbox"),
                    ConsentStatement(text: "allow-list only", expected: false, tierOrigin: "container"),
                  ])
    }

    @Test func startsDisarmed() {
        let m = HostConsentModel(preflight())
        #expect(m.allMatched == false)
        #expect(m.wrongRowID == nil)
    }

    @Test func armsWhenEveryAnswerMatchesGroundTruth() {
        let m = HostConsentModel(preflight())
        for s in m.statements { m.answer(s.id, s.expected) }
        #expect(m.allMatched == true)
        #expect(m.wrongRowID == nil)
    }

    @Test func wrongAnswerDisarmsAndClearsEverything() {
        let m = HostConsentModel(preflight())
        let ids = m.statements
        m.answer(ids[0].id, ids[0].expected)
        m.answer(ids[1].id, ids[1].expected)
        m.answer(ids[2].id, !ids[2].expected)            // one wrong
        #expect(m.allMatched == false)
        #expect(m.wrongRowID == ids[2].id)
        #expect(m.statements.allSatisfy { $0.answer == nil })  // every answer cleared
    }

    @Test func recoversAfterAWrongAnswer() {
        let m = HostConsentModel(preflight())
        let first = m.statements[0]
        m.answer(first.id, !first.expected)              // wrong
        #expect(m.wrongRowID != nil)
        for s in m.statements { m.answer(s.id, s.expected) }   // clean pass
        #expect(m.allMatched == true)
        #expect(m.wrongRowID == nil)
    }
}
