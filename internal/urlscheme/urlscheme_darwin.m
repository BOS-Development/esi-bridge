// Apple Event handler for kAEGetURL: this is how macOS delivers a
// eveauth-proxy:// URL to ESI Bridge when it's already running, instead of
// spawning a new process (which is what Windows/Linux do). It's registered
// independently of fyne.io/systray's own NSApplication delegate (see
// systray_darwin.m), so it doesn't conflict with it — NSAppleEventManager
// handlers aren't tied to the app delegate.
#import <Cocoa/Cocoa.h>
#include "_cgo_export.h"

@interface ESIProxyURLEventHandler : NSObject
- (void)handleGetURLEvent:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)replyEvent;
@end

@implementation ESIProxyURLEventHandler
- (void)handleGetURLEvent:(NSAppleEventDescriptor *)event withReplyEvent:(NSAppleEventDescriptor *)replyEvent {
  NSString *urlString = [[event paramDescriptorForKeyword:keyDirectObject] stringValue];
  goHandleAuthCallbackURL((char *)[urlString UTF8String]);
}
@end

static ESIProxyURLEventHandler *esiProxyURLEventHandler;

void esiproxy_installURLEventHandler(void) {
  // Dispatched onto the main queue for two reasons: it's the safe way to
  // touch Cocoa-adjacent APIs regardless of which thread calls this, and —
  // more importantly — it must run *after* NSApplication has finished its
  // own startup (which installs a default kAEGetURL handler of its own),
  // so ours registers last and wins instead of getting silently overwritten.
  dispatch_async(dispatch_get_main_queue(), ^{
    esiProxyURLEventHandler = [[ESIProxyURLEventHandler alloc] init];
    [[NSAppleEventManager sharedAppleEventManager]
        setEventHandler:esiProxyURLEventHandler
            andSelector:@selector(handleGetURLEvent:withReplyEvent:)
          forEventClass:kInternetEventClass
             andEventID:kAEGetURL];
  });
}
