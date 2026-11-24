import SwiftUI

@main struct ProbeApp: App {
    var body: some Scene {
        WindowGroup { Color.clear }
    }
}

// The probe has no interface strings or full app theme. Rendering sources and colors
// come from Persona, and these size and timing values match its production tokens.
nonisolated enum StateName: String {
    case working, needsYou
    var text: String { rawValue }
}

nonisolated enum Theme {
    enum Motion {
        static let personaFrameInterval: TimeInterval = 1 / 15
    }
    enum Size {
        static let avatar: CGFloat = 40
        static let tapTarget: CGFloat = 44
        static let avatarL: CGFloat = 46
        static let searchField: CGFloat = 44
        static let callAvatar: CGFloat = 160
        static let callAvatarFace: CGFloat = 0.78
    }
}
