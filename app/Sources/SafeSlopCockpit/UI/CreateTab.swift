import SwiftUI

/// CreateTab will host the dual visual+text profile editor, the safety arbiter, and the file/network
/// scope editors (specs/0029 Tasks 2–4). It is a placeholder until those land; shown now so the
/// three-tab shell is complete and navigable.
struct CreateTab: View {
    var body: some View {
        ContentUnavailableView {
            Label("Create a profile", systemImage: "plus.square.on.square")
        } description: {
            Text("The dual visual + CUE editor, the safety arbiter, and drag-drop file/network scopes "
                 + "land here next (specs/0029). For now, edit safeslop.cue directly, then Launch.")
        }
        .padding()
    }
}
