import SwiftUI
import UniformTypeIdentifiers

/// FileScopeEditor builds a `files: { read, write, deny }` CUE block by dragging folders in — no path
/// typing (research S9). It is intentionally NON-DESTRUCTIVE: it GENERATES a snippet to paste into a
/// profile rather than rewriting the canonical CUE text, so it can't corrupt hand-written policy
/// (the text-canonical rule) and doesn't depend on the deferred visual-authoring decision (specs/0029).
struct FileScopeEditor: View {
    /// When set, a "Merge into <profile>" button calls this with the generated snippet so the host
    /// can splice it into the canonical CUE text. mergeLabel names the target; mergeEnabled gates it.
    var mergeLabel: String = ""
    var mergeEnabled: Bool = false
    var onMerge: ((String) -> Void)? = nil

    @State private var read: [String] = []
    @State private var write: [String] = []
    @State private var deny: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Drag folders into a lane, then paste the snippet into a profile.")
                .font(.caption).foregroundStyle(.secondary)
            lane("read", "eye", .gray, $read)
            lane("write", "pencil", .orange, $write)
            lane("deny", "hand.raised", .red, $deny)

            if !snippet.isEmpty {
                Text("CUE — paste inside a profile:").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Text(snippet).font(.caption.monospaced()).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8).background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 6))
                if !read.isEmpty || !write.isEmpty {
                    Label("Credential stores (~/.ssh keys, ~/.aws, …) are auto-denied for any granted scope.",
                          systemImage: "checkmark.shield")
                        .font(.caption2).foregroundStyle(.green)
                }
                HStack {
                    if let onMerge {
                        Button(mergeLabel.isEmpty ? "Merge" : "Merge into \(mergeLabel)", systemImage: "arrow.down.doc") {
                            onMerge(snippet); read = []; write = []; deny = []
                        }
                        .buttonStyle(.borderedProminent)
                        .disabled(!mergeEnabled)
                        .help(mergeEnabled ? "splice this files: block into \(mergeLabel)"
                                           : "select a profile that has no files: block yet")
                    }
                    Button("Copy", systemImage: "doc.on.doc") { copy() }
                    Button("Clear", role: .destructive) { read = []; write = []; deny = [] }
                    Spacer()
                }
            }
        }
    }

    @ViewBuilder
    private func lane(_ title: String, _ icon: String, _ color: Color, _ paths: Binding<[String]>) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Label(title, systemImage: icon).font(.caption.weight(.semibold)).foregroundStyle(color)
            VStack(alignment: .leading, spacing: 2) {
                if paths.wrappedValue.isEmpty {
                    Text("drop folders here").font(.caption2).foregroundStyle(.tertiary)
                        .frame(maxWidth: .infinity, alignment: .center).padding(.vertical, 6)
                } else {
                    ForEach(paths.wrappedValue, id: \.self) { p in
                        HStack {
                            Text(p).font(.caption.monospaced()).lineLimit(1).truncationMode(.middle)
                            Spacer()
                            Button { paths.wrappedValue.removeAll { $0 == p } } label: {
                                Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary)
                            }.buttonStyle(.plain)
                        }
                    }
                }
            }
            .padding(6)
            .frame(maxWidth: .infinity)
            .background(color.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
            .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(color.opacity(0.3), style: StrokeStyle(lineWidth: 1, dash: [4])))
            .dropDestination(for: URL.self) { urls, _ in
                for u in urls where u.hasDirectoryPath || u.isFileURL {
                    let h = homeify(u.path)
                    if !paths.wrappedValue.contains(h) { paths.wrappedValue.append(h) }
                }
                return true
            }
        }
    }

    /// Paths under $HOME render as ~-relative (portable + matches the auto-deny convention).
    private func homeify(_ path: String) -> String {
        let home = NSHomeDirectory()
        if path == home { return "~" }
        if path.hasPrefix(home + "/") { return "~" + path.dropFirst(home.count) }
        return path
    }

    private func cueList(_ xs: [String]) -> String {
        "[" + xs.map { "\"\($0)\"" }.joined(separator: ", ") + "]"
    }

    private var snippet: String {
        var lines: [String] = []
        if !read.isEmpty { lines.append("\tread: \(cueList(read))") }
        if !write.isEmpty { lines.append("\twrite: \(cueList(write))") }
        if !deny.isEmpty { lines.append("\tdeny: \(cueList(deny))") }
        guard !lines.isEmpty else { return "" }
        return "files: {\n" + lines.joined(separator: "\n") + "\n}"
    }

    private func copy() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(snippet, forType: .string)
    }
}
