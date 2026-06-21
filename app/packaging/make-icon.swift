#!/usr/bin/env swift
// Generates the SafeSlop cockpit app icon: a green "safety shield" (containment) with a terminal
// prompt `>_` (the sandboxed agent) on a dark squircle. Renders a 1024×1024 PNG.
//   swift app/packaging/make-icon.swift out-1024.png
// app/packaging/make-icns.sh turns that into SafeSlop.icns.
import AppKit

let S: CGFloat = 1024
let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "icon-1024.png"
func rgb(_ r: Double, _ g: Double, _ b: Double) -> NSColor { NSColor(srgbRed: r, green: g, blue: b, alpha: 1) }

let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(S), pixelsHigh: Int(S),
                           bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                           colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
NSGraphicsContext.saveGraphicsState()
let gctx = NSGraphicsContext(bitmapImageRep: rep)!
NSGraphicsContext.current = gctx
let ctx = gctx.cgContext

// — dark squircle background —
let margin = S * 0.092
let bg = NSRect(x: margin, y: margin, width: S - 2*margin, height: S - 2*margin)
let bgRadius = bg.width * 0.2237
let bgPath = NSBezierPath(roundedRect: bg, xRadius: bgRadius, yRadius: bgRadius)
bgPath.addClip()
NSGradient(colors: [rgb(0.12, 0.16, 0.19), rgb(0.06, 0.08, 0.10)])!.draw(in: bg, angle: -90)
rgb(1,1,1).withAlphaComponent(0.05).setFill()
NSBezierPath(roundedRect: NSRect(x: bg.minX, y: bg.midY, width: bg.width, height: bg.height/2),
             xRadius: bgRadius, yRadius: bgRadius).fill()

// — safety shield —
let cx = S/2
let top = S*0.80, bot = S*0.185
let halfW = S*0.255
let shoulder = S*0.60          // where the flat top transitions into the curving sides
let cr = S*0.035   // top-corner radius
let shield = CGMutablePath()
shield.move(to: CGPoint(x: cx - halfW + cr, y: top))
shield.addLine(to: CGPoint(x: cx + halfW - cr, y: top))                              // flat top edge
shield.addQuadCurve(to: CGPoint(x: cx + halfW, y: top - cr), control: CGPoint(x: cx + halfW, y: top))
shield.addLine(to: CGPoint(x: cx + halfW, y: shoulder))                              // straight shoulder
shield.addCurve(to: CGPoint(x: cx, y: bot),                                          // right side -> tip
                control1: CGPoint(x: cx + halfW, y: S*0.40),
                control2: CGPoint(x: cx + halfW*0.62, y: S*0.255))
shield.addCurve(to: CGPoint(x: cx - halfW, y: shoulder),                             // tip -> left side
                control1: CGPoint(x: cx - halfW*0.62, y: S*0.255),
                control2: CGPoint(x: cx - halfW, y: S*0.40))
shield.addLine(to: CGPoint(x: cx - halfW, y: top - cr))
shield.addQuadCurve(to: CGPoint(x: cx - halfW + cr, y: top), control: CGPoint(x: cx - halfW, y: top))
shield.closeSubpath()
// round the two top corners by re-stroking the path with a round join over a clip
ctx.saveGState()
ctx.addPath(shield); ctx.clip()
NSGradient(colors: [rgb(0.30, 0.80, 0.46), rgb(0.13, 0.55, 0.42)])!     // green -> teal
    .draw(in: NSRect(x: cx-halfW, y: bot, width: halfW*2, height: top-bot), angle: -90)
// inner top sheen
rgb(1,1,1).withAlphaComponent(0.12).setFill()
NSBezierPath(rect: NSRect(x: cx-halfW, y: (top+bot)/2, width: halfW*2, height: (top-bot)/2)).fill()
ctx.restoreGState()
// crisp rim
ctx.addPath(shield)
ctx.setStrokeColor(rgb(1,1,1).withAlphaComponent(0.22).cgColor)
ctx.setLineWidth(10); ctx.setLineJoin(.round); ctx.strokePath()

// — terminal prompt `>_` inside the shield —
ctx.setStrokeColor(rgb(0.98, 0.99, 0.98).cgColor)
ctx.setLineWidth(46); ctx.setLineCap(.round); ctx.setLineJoin(.round)
ctx.setShadow(offset: .zero, blur: 18, color: rgb(0,0,0).withAlphaComponent(0.35).cgColor)
let gx = cx - S*0.085, gy = S*0.52, ch = S*0.105, cw = S*0.115
ctx.move(to: CGPoint(x: gx, y: gy + ch))          // chevron ">"
ctx.addLine(to: CGPoint(x: gx + cw, y: gy))
ctx.addLine(to: CGPoint(x: gx, y: gy - ch))
ctx.strokePath()
// underscore cursor to the right
ctx.move(to: CGPoint(x: gx + cw*1.25, y: gy - ch))
ctx.addLine(to: CGPoint(x: gx + cw*1.25 + S*0.14, y: gy - ch))
ctx.strokePath()
ctx.setShadow(offset: .zero, blur: 0, color: nil)

NSGraphicsContext.restoreGraphicsState()
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: out))
print("wrote \(out)")
