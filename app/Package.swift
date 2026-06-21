// swift-tools-version: 6.0
import PackageDescription

// SafeSlop cockpit — native SwiftUI app that embeds a SwiftTerm terminal and drives the
// safeslop engine over gRPC-on-UDS (specs/0014). The gRPC client is generated from the
// committed control.proto by the grpc-swift-protobuf build plugin (protoc must be on PATH).
let package = Package(
    name: "SafeSlopCockpit",
    platforms: [.macOS(.v15)],
    dependencies: [
        .package(url: "https://github.com/grpc/grpc-swift-2.git", from: "2.0.0"),
        .package(url: "https://github.com/grpc/grpc-swift-nio-transport.git", from: "2.0.0"),
        .package(url: "https://github.com/grpc/grpc-swift-protobuf.git", from: "2.0.0"),
        .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.28.0"),
        .package(url: "https://github.com/migueldeicaza/SwiftTerm.git", from: "1.2.0"),
    ],
    targets: [
        .executableTarget(
            name: "SafeSlopCockpit",
            dependencies: [
                .product(name: "GRPCCore", package: "grpc-swift-2"),
                .product(name: "GRPCNIOTransportHTTP2", package: "grpc-swift-nio-transport"),
                .product(name: "GRPCProtobuf", package: "grpc-swift-protobuf"),
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
                .product(name: "SwiftTerm", package: "SwiftTerm"),
            ],
            // control.proto + grpc-swift-proto-generator-config.json live under Sources/.../proto.
            // The plugin regenerates the Swift client at build time, so no generated code is committed.
            plugins: [
                .plugin(name: "GRPCProtobufGenerator", package: "grpc-swift-protobuf")
            ]
        ),
        // Headless model-layer tests (`swift test`): EngineModel's state machine via a fake EngineClient
        // and the pure ProfileRef logic that drives the views (tier ordering, risk→color, trust/network
        // honesty). No window server — the SwiftUI `--mount-check` analog.
        .testTarget(
            name: "SafeSlopCockpitTests",
            dependencies: ["SafeSlopCockpit"]
        ),
    ]
)
