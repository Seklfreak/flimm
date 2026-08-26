import SwiftUI

/// The three states every list can be in besides "has rows", sized for a
/// screen someone is looking at from a sofa.
struct TVLoadingState: View {
    var label: String = "Loading…"

    var body: some View {
        VStack(spacing: 20) {
            ProgressView()
            Text(label)
                .font(.title3)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct TVEmptyState: View {
    let icon: String
    let title: String
    var message: String?
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: icon)
                .font(.system(size: 72))
                .foregroundStyle(.tertiary)
            Text(title)
                .font(.title2.bold())
            if let message {
                Text(message)
                    .font(.title3)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 900)
            }
            if let actionTitle, let action {
                Button(actionTitle, action: action)
                    .padding(.top, 8)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(.horizontal, TVMetrics.margin)
    }
}

struct TVErrorState: View {
    let message: String
    var retry: (() -> Void)?

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 64))
                .foregroundStyle(Palette.danger)
            Text("Something went wrong")
                .font(.title2.bold())
            Text(message)
                .font(.title3)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 900)
            if let retry {
                Button("Try again", action: retry)
                    .padding(.top, 8)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(.horizontal, TVMetrics.margin)
    }
}

/// The blue count pill next to a feed or channel name.
struct TVUnseenBadge: View {
    let count: Int

    var body: some View {
        if count > 0 {
            Text(Fmt.count(count))
                .font(.caption.weight(.bold))
                .foregroundStyle(.white)
                .padding(.horizontal, 10)
                .padding(.vertical, 3)
                .background(Palette.accent, in: Capsule())
        }
    }
}

/// A screen title in the TV's own proportions. `navigationTitle` is not shown
/// by a tvOS `NavigationStack` under a tab bar, so screens draw their own.
struct TVScreenTitle: View {
    let title: String
    var subtitle: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.system(size: 56, weight: .bold))
            if let subtitle {
                Text(subtitle)
                    .font(.title3)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
