import SwiftUI

/// RiskBadge is a colored "ecusson" — a rounded chip whose BACKGROUND carries the danger level
/// (red = high / orange = elevated / green = contained), with the tier glyph on top in white.
/// Decoupling the danger *color* (the chip) from the *glyph* (the icon) means the color signal is
/// unambiguous and we're free to pick icons for meaning rather than for their tint (jojo's ask).
struct RiskBadge: View {
    let symbol: String
    let color: Color
    var size: CGFloat = 30

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.24, style: .continuous)
                .fill(color.gradient)
            Image(systemName: symbol)
                .font(.system(size: size * 0.5, weight: .semibold))
                .foregroundStyle(.white)
        }
        .frame(width: size, height: size)
        // a faint same-color halo lifts the chip off the row background.
        .shadow(color: color.opacity(0.35), radius: 2, y: 1)
    }
}
