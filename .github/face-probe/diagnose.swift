import SwiftUI
import Testing
import UIKit
@testable import Persona

@MainActor struct FaceLayoutDiagnostic {
    @Test func layoutAfterHostingPass() async throws {
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(x: 0, y: 0, width: 128, height: 128)
        let view = PersonaFaceMotion(face: PersonaFace(size: 40, design: .mochi, state: .working), animating: false)
        let host = UIHostingController(rootView: view.frame(width: 40, height: 40))
        host.safeAreaRegions = []
        window.rootViewController = host
        window.makeKeyAndVisible()
        defer { window.isHidden = true }
        window.layoutIfNeeded()
        try await Task.sleep(for: .milliseconds(250))
        window.layoutIfNeeded()
        let motion = try #require(findMotion(under: host.view))
        for (name, item) in [("motion", motion as UIView), ("arc", motion.arc as UIView), ("body", motion.body),
                             ("body phase", motion.workingBodies[0] as UIView), ("eyes", motion.eyes as UIView)] {
            print("Face layout \(name): bounds=\(item.bounds) parent=\(String(describing: item.superview?.bounds)) subview=\(String(describing: item.subviews.first?.bounds))")
        }
    }

    private func findMotion(under root: UIView) -> PersonaFaceMotionView? {
        if let motion = root as? PersonaFaceMotionView { return motion }
        return root.subviews.lazy.compactMap { findMotion(under: $0) }.first
    }
}
