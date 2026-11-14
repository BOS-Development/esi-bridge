// Promotes the window process to a regular foreground macOS app at runtime.
//
// The .app bundle is shared with the tray process and marks it LSUIElement (a
// background menu-bar agent with no Dock icon). This window process reuses the
// same bundle, so it would otherwise inherit that accessory policy — the
// webview's own activation code only promotes *non-bundled* apps. Setting the
// policy to Regular here gives the window a Dock icon, an app menu and a
// normal focusable window; activateIgnoringOtherApps brings it to the front.
#import <Cocoa/Cocoa.h>

void esiproxy_makeForegroundApp(void) {
  [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
  [NSApp activateIgnoringOtherApps:YES];
}
