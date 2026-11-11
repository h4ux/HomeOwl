// +build darwin

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <CoreGraphics/CoreGraphics.h>
#include "menubar.h"

static NSStatusItem *item;
static NSPopover *pop;
static WKWebView *web;
static NSImage *base;
static BOOL baseTemplate = YES, withDot = NO;
static int lvl = 0;
static NSString *title = @"";

@interface HOTarget : NSObject
@end
@implementation HOTarget
- (void)click:(id)sender {
	if (pop.shown) { [pop performClose:nil]; return; }
	[web evaluateJavaScript:@"window.homeowlShown && window.homeowlShown()" completionHandler:nil];
	[pop showRelativeToRect:item.button.bounds ofView:item.button preferredEdge:NSRectEdgeMinY];
	[NSApp activateIgnoringOtherApps:YES]; // the popover closes on a click elsewhere only while HomeOwl is active
}
@end
static HOTarget *target;

int hoGUIAvailable(void) {
	CFDictionaryRef d = CGSessionCopyCurrentDictionary();
	if (d == NULL) return 0;
	CFRelease(d);
	return 1;
}

static NSColor *levelColor(void) {
	return lvl >= 2 ? [NSColor systemRedColor] : [NSColor systemOrangeColor];
}

static void apply(void) {
	if (item == nil || base == nil) return;
	NSImage *img = base;
	if (withDot && lvl > 0) {
		NSImage *owl = base;
		img = [NSImage imageWithSize:NSMakeSize(18, 18) flipped:NO drawingHandler:^BOOL(NSRect r) {
			// the owl in the menubar's text colour (follows light/dark and the selected state), plus a coloured dot
			NSImage *tinted = [NSImage imageWithSize:r.size flipped:NO drawingHandler:^BOOL(NSRect rr) {
				[owl drawInRect:rr];
				[[NSColor labelColor] set];
				NSRectFillUsingOperation(rr, NSCompositingOperationSourceAtop);
				return YES;
			}];
			[tinted drawInRect:r];
			NSRect dot = NSMakeRect(r.size.width - 7.5, 0.5, 7, 7);
			[[NSGraphicsContext currentContext] saveGraphicsState];
			[NSGraphicsContext currentContext].compositingOperation = NSCompositingOperationClear;
			[[NSBezierPath bezierPathWithOvalInRect:NSInsetRect(dot, -1.2, -1.2)] fill];
			[[NSGraphicsContext currentContext] restoreGraphicsState];
			[levelColor() set];
			[[NSBezierPath bezierPathWithOvalInRect:dot] fill];
			return YES;
		}];
		img.template = NO;
	}
	item.button.image = img;
	item.button.imagePosition = title.length ? NSImageLeft : NSImageOnly;
	item.button.title = title;
}

static void makeItem(void) {
	if (item != nil) return;
	item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
	item.button.target = target;
	item.button.action = @selector(click:);
	item.button.toolTip = @"HomeOwl";
	apply();
}

void hoInit(const char *url, int show) {
	NSString *u = [NSString stringWithUTF8String:url];
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory]; // no Dock icon, no menu bar of its own
	target = [HOTarget new];
	WKWebViewConfiguration *cfg = [WKWebViewConfiguration new];
	cfg.websiteDataStore = [WKWebsiteDataStore nonPersistentDataStore];
	web = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 340, 420) configuration:cfg];
	[web loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:u]]];
	NSViewController *vc = [NSViewController new];
	vc.view = web;
	pop = [NSPopover new];
	pop.contentViewController = vc;
	pop.contentSize = NSMakeSize(340, 420);
	pop.behavior = NSPopoverBehaviorTransient;
	pop.animates = YES;
	if (show) makeItem();
}

void hoRun(void) { [NSApp run]; }

void hoSetVisible(int show) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (show) { makeItem(); return; }
		if (item == nil) return;
		[pop performClose:nil];
		[[NSStatusBar systemStatusBar] removeStatusItem:item];
		item = nil;
	});
}

void hoSetIcon(const void *png, int len, int tmpl, int dot, int level, const char *t) {
	NSData *data = [NSData dataWithBytes:png length:len];
	NSString *s = [NSString stringWithUTF8String:t];
	dispatch_async(dispatch_get_main_queue(), ^{
		NSImage *img = [[NSImage alloc] initWithData:data];
		img.size = NSMakeSize(18, 18);
		img.template = tmpl ? YES : NO;
		base = img; baseTemplate = tmpl; withDot = dot; lvl = level; title = s;
		apply();
	});
}

void hoClosePopover(void) {
	dispatch_async(dispatch_get_main_queue(), ^{ [pop performClose:nil]; });
}

void hoSetHeight(int h) {
	if (h < 200) h = 200;
	if (h > 760) h = 760;
	dispatch_async(dispatch_get_main_queue(), ^{ pop.contentSize = NSMakeSize(340, h); });
}
