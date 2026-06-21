import SwiftUI

/// InstallConsentSheet is the proportionate consent gate shown before an install runs (specs/0037).
/// It surfaces the preview the engine computed (tools.InstallPreview, carried on ToolStatus): the
/// verification posture (incl. whether the pin's checksum is vendor-published or a trust-on-first-use
/// hash), the precautions safeslop takes, and the exact command. A verified-pin, brew, or confined
/// verified-installer is a single click; the higher-blast-radius routes — an UNVERIFIED remote script
/// (curl|sh / npm) OR an UNCONFINED admin installer (e.g. nix as root) — require the user to type the
/// tool's name first, friction proportionate to the risk. The gate only previews; the actual install
/// stays the existing InstallTool stream.
struct InstallConsentSheet: View {
    let tool: Safeslop_Control_V1_ToolStatus
    let onConfirm: () -> Void
    let onCancel: () -> Void
    @State private var typed = ""

    /// gated = the higher-blast-radius routes that demand a typed confirm: an unverified remote script OR
    /// an unconfined admin installer (e.g. nix runs as root). Verified-pin/brew/confined installers are
    /// one-click. (Mirrors the engine's Preview.NeedsConsent, which now covers both, specs/0037.)
    private var gated: Bool { tool.needsConsent }
    /// One-click routes arm immediately; a gated route arms only once the user retypes the tool name.
    private var armed: Bool { !gated || typed.trimmingCharacters(in: .whitespaces) == tool.name }
    /// The pin/installer checksum had no vendor-published source — it is safeslop's own trust-on-first-use
    /// hash. Surfaced as a distinct, cautionary badge so "verified" never reads as vendor-cross-checked.
    private var tofu: Bool { tool.provenance == "tls" }
    /// Why this install is gated, phrased for the actual route (a remote script vs an unconfined installer).
    private var gateReason: String {
        tool.verification == "verified-installer"
            ? "This installer runs UNCONFINED with administrator privileges."
            : "This runs a remote script with your privileges."
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Install \(tool.name)").font(.title3.weight(.semibold))
                if !tool.note.isEmpty {
                    Text(tool.note).font(.callout).foregroundStyle(.secondary)
                }
            }

            badge
            Text(tool.precautions).font(.callout).fixedSize(horizontal: false, vertical: true)

            VStack(alignment: .leading, spacing: 4) {
                Text("Command").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Text(tool.installHint.isEmpty ? "—" : tool.installHint)
                    .font(.caption.monospaced()).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                    .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 6))
                if tool.verification == "verified-pin" || tool.verification == "verified-installer" { provenance }
            }

            if gated {
                VStack(alignment: .leading, spacing: 4) {
                    Text("\(gateReason) Type “\(tool.name)” to confirm you read the precautions above.")
                        .font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                    TextField("type \(tool.name)", text: $typed)
                        .textFieldStyle(.roundedBorder).frame(maxWidth: 220)
                }
            }

            HStack {
                Button("Cancel", role: .cancel, action: onCancel)
                Spacer()
                Button(gated ? "Run install" : "Install", action: onConfirm)
                    .buttonStyle(.borderedProminent)
                    .tint(gated ? .orange : .accentColor)
                    .disabled(!armed)
                    .keyboardShortcut(armed && !gated ? .defaultAction : nil)
            }
        }
        .padding(20)
        .frame(width: 460)
    }

    @ViewBuilder private var badge: some View {
        switch tool.verification {
        case "verified-pin":
            tofu ? pill("sha256-pinned · no vendor checksum", "exclamationmark.shield.fill", .yellow)
                 : pill("sha256-verified pin", "checkmark.seal.fill", .green)
        case "verified-installer":
            tofu ? pill("sha256-pinned installer · no vendor checksum", "exclamationmark.shield.fill", .yellow)
                 : pill("sha256-verified installer", "checkmark.seal.fill", .teal)
        case "brew": pill("installed via Homebrew", "mug.fill", .blue)
        case "unverified-run": pill("unverified remote script", "exclamationmark.triangle.fill", .orange)
        default: EmptyView()
        }
    }

    private func pill(_ text: String, _ icon: String, _ color: Color) -> some View {
        Label(text, systemImage: icon)
            .font(.caption.weight(.semibold)).foregroundStyle(color)
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(color.opacity(0.14), in: Capsule())
    }

    @ViewBuilder private var provenance: some View {
        VStack(alignment: .leading, spacing: 2) {
            if !tool.sourceURL.isEmpty {
                Text("source: \(tool.sourceURL)")
                    .font(.caption2).foregroundStyle(.secondary).textSelection(.enabled)
                    .lineLimit(1).truncationMode(.middle)
            }
            HStack(spacing: 10) {
                if !tool.pinnedVersion.isEmpty { Text("version \(tool.pinnedVersion)") }
                if tool.sha256.count >= 12 { Text("sha256 \(tool.sha256.prefix(12))…") }
            }
            .font(.caption2).foregroundStyle(.secondary)
        }
    }
}
