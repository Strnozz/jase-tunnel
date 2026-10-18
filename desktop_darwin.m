//go:build darwin && cgo

#import <AppKit/AppKit.h>
#import <UserNotifications/UserNotifications.h>

extern void jaseDesktopQuit(void);
extern char *jaseDesktopState(unsigned long long);
extern void jaseDesktopAction(char *, int);
extern void jaseDesktopImport(char *, int);
extern void jaseDesktopImportFound(char *);
extern char *jaseDesktopApproval(void);
extern void jaseDesktopConsent(char *,char *);
extern void jaseDesktopNotificationClicked(char *);
extern void jaseDesktopRevokeSite(char *);

// Artwork comes from assets/icon.svg, rasterized by the package builder.
// The app keeps its colors; the menu bar uses the icon's white silhouette.
static NSImage *JaseIcon(CGFloat size, BOOL menu) {
    NSString *name=menu?@"JaseMenu":@"Jase";
    NSImage *source=[NSImage imageNamed:name];
    if (!source) source=[NSImage imageNamed:NSImageNameApplicationIcon];
    NSImage *image=[[source copy] autorelease];image.size=NSMakeSize(size,size);image.template=NO;return image;
}

// Native AppKit controls throughout: no embedded browser or local UI server.
static NSTextField *JaseLabel(NSString *text, CGFloat size, NSFontWeight weight) {
    NSTextField *label=[NSTextField labelWithString:text];
    label.font=[NSFont systemFontOfSize:size weight:weight];
    label.lineBreakMode=NSLineBreakByTruncatingTail;
    return label;
}
static NSStackView *JaseStack(NSArray<NSView *> *views, NSUserInterfaceLayoutOrientation orientation) {
    NSStackView *stack=[NSStackView stackViewWithViews:views];
    stack.orientation=orientation;stack.spacing=12;
    stack.alignment=orientation==NSUserInterfaceLayoutOrientationVertical?NSLayoutAttributeLeading:NSLayoutAttributeCenterY;
    return stack;
}
static void JasePin(NSView *child,NSView *parent,CGFloat inset) {
    child.translatesAutoresizingMaskIntoConstraints=NO;[parent addSubview:child];
    [NSLayoutConstraint activateConstraints:@[
        [child.leadingAnchor constraintEqualToAnchor:parent.leadingAnchor constant:inset],
        [child.trailingAnchor constraintEqualToAnchor:parent.trailingAnchor constant:-inset],
        [child.topAnchor constraintEqualToAnchor:parent.topAnchor constant:inset],
        [child.bottomAnchor constraintEqualToAnchor:parent.bottomAnchor constant:-inset]]];
}
static NSView *JaseSpacer(void) {
    NSView *view=[[[NSView alloc] init] autorelease];
    [view setContentHuggingPriority:1 forOrientation:NSLayoutConstraintOrientationHorizontal];return view;
}

@interface JaseDesktopDelegate : NSObject <NSApplicationDelegate,NSWindowDelegate,NSTableViewDataSource,NSTableViewDelegate,UNUserNotificationCenterDelegate>
@property(retain) NSStatusItem *statusItem;
@property(retain) NSMenuItem *addMenuItem;
@property(retain) NSWindow *window;
@property(retain) NSTabView *tabs;
@property(retain) NSTextField *statusTitle;
@property(retain) NSTextField *statusDetail;
@property(retain) NSImageView *statusIcon;
@property(retain) NSTextField *statistics;
@property(retain) NSTextField *privacy;
@property(retain) NSTextField *notice;
@property(retain) NSTextField *version;
@property(retain) NSButton *debugButton;
@property(retain) NSButton *thinkingButton;
@property(retain) NSButton *addButton;
@property(retain) NSButton *importButton;
@property(retain) NSProgressIndicator *progress;
@property(retain) NSPopUpButton *filter;
@property(retain) NSTableView *modelTable;
@property(retain) NSTableView *logTable;
@property(retain) NSTableView *siteTable;
@property(retain) NSArray *sites;
@property(retain) NSString *selectedSite;
@property(retain) NSButton *revokeSiteButton;
@property(retain) NSTextView *detail;
@property(retain) NSArray *models;
@property(retain) NSMutableArray *events;
@property(retain) NSArray *filteredEvents;
@property(retain) NSTimer *timer;
@property(retain) NSISO8601DateFormatter *dates;
@property(retain) NSDateFormatter *clock;
@property unsigned long long cursor;
@property unsigned long long generation;
@property BOOL firstState;
@property BOOL importing;
@property BOOL consenting;
- (void)setupInterface;
@end

@implementation JaseDesktopDelegate
- (NSButton *)button:(NSString *)title action:(SEL)action {
    NSButton *button=[NSButton buttonWithTitle:title target:self action:action];button.bezelStyle=NSBezelStyleRounded;return button;
}
- (NSScrollView *)table:(NSTableView **)table columns:(NSArray *)columns {
    NSTableView *view=[[[NSTableView alloc] init] autorelease];
    view.delegate=self;view.dataSource=self;view.rowHeight=28;view.usesAlternatingRowBackgroundColors=YES;
    view.style=NSTableViewStyleInset;view.columnAutoresizingStyle=NSTableViewLastColumnOnlyAutoresizingStyle;
    for (NSArray *column in columns) {
        NSTableColumn *item=[[[NSTableColumn alloc] initWithIdentifier:column[0]] autorelease];
        item.title=column[1];item.width=[column[2] doubleValue];item.minWidth=60;[view addTableColumn:item];
    }
    NSScrollView *scroll=[[[NSScrollView alloc] init] autorelease];scroll.documentView=view;
    scroll.hasVerticalScroller=YES;scroll.autohidesScrollers=YES;scroll.borderType=NSBezelBorder;
    [scroll.heightAnchor constraintGreaterThanOrEqualToConstant:140].active=YES;
    *table=view;return scroll;
}
- (NSView *)modelPane {
    NSTextField *intro=JaseLabel(@"Models and weights found on this computer",13,NSFontWeightSemibold);
    NSTableView *table=nil;
    NSScrollView *scroll=[self table:&table columns:@[@[@"name",@"Model",@330],@[@"engine",@"Engine / format",@130],@[@"status",@"Status",@180]]];
    self.modelTable=table;table.target=self;table.doubleAction=@selector(importSelected:);
    self.addButton=[self button:@"Add weights…" action:@selector(addWeights:)];
    self.addButton.image=[NSImage imageWithSystemSymbolName:@"plus" accessibilityDescription:nil];self.addButton.imagePosition=NSImageLeft;
    self.importButton=[self button:@"Add selected" action:@selector(importSelected:)];self.importButton.enabled=NO;
    self.progress=[[[NSProgressIndicator alloc] init] autorelease];self.progress.style=NSProgressIndicatorStyleSpinning;self.progress.controlSize=NSControlSizeSmall;self.progress.displayedWhenStopped=NO;
    NSStackView *buttons=JaseStack(@[self.addButton,self.importButton,self.progress,JaseSpacer()],NSUserInterfaceLayoutOrientationHorizontal);
    self.notice=[NSTextField wrappingLabelWithString:@"Open Ollama or the LM Studio local server to make models available. You can add GGUF files or compatible weights directories."];
    self.notice.font=[NSFont systemFontOfSize:11];self.notice.textColor=NSColor.secondaryLabelColor;
    NSStackView *content=JaseStack(@[intro,scroll,buttons,self.notice],NSUserInterfaceLayoutOrientationVertical);
    NSView *pane=[[[NSView alloc] init] autorelease];JasePin(content,pane,14);
    for(NSView *child in @[scroll,buttons,self.notice]) [child.widthAnchor constraintEqualToAnchor:content.widthAnchor].active=YES;
    return pane;
}
// Left: every site with its tunnel state. Right: the console of the selected site.
- (NSView *)activityPane {
    NSTableView *sites=nil;
    NSScrollView *siteScroll=[self table:&sites columns:@[@[@"site",@"Websites",@200]]];
    self.siteTable=sites;sites.rowHeight=40;sites.usesAlternatingRowBackgroundColors=NO;[sites setAccessibilityLabel:@"Websites"];
    [siteScroll.widthAnchor constraintEqualToConstant:220].active=YES;
    self.revokeSiteButton=[self button:@"Disconnect website" action:@selector(revokeSite:)];self.revokeSiteButton.enabled=NO;
    self.statistics=JaseLabel(@"No activity",12,NSFontWeightRegular);self.statistics.textColor=NSColor.secondaryLabelColor;
    self.filter=[[[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO] autorelease];
    for(NSArray *option in @[@[@"All activity",@"all"],@[@"Server requests",@"server"],@[@"Server responses",@"receive"],@[@"Model and interpreter",@"model"],@[@"Errors",@"error"]]) {
        [self.filter addItemWithTitle:option[0]];self.filter.lastItem.representedObject=option[1];
    }
    [self.filter setAccessibilityLabel:@"Filter log"];self.filter.target=self;self.filter.action=@selector(filterChanged:);
    NSStackView *toolbar=JaseStack(@[self.filter,JaseSpacer(),self.revokeSiteButton,[self button:@"Clear log" action:@selector(clearLog:)]],NSUserInterfaceLayoutOrientationHorizontal);
    NSTableView *table=nil;
    NSScrollView *scroll=[self table:&table columns:@[@[@"time",@"Time",@70],@[@"event",@"Activity",@330]]];self.logTable=table;
    [table setAccessibilityLabel:@"Tunnel log"];
    NSScrollView *inspector=[[[NSScrollView alloc] init] autorelease];inspector.hasVerticalScroller=YES;inspector.autohidesScrollers=YES;inspector.borderType=NSBezelBorder;
    self.detail=[[[NSTextView alloc] initWithFrame:NSMakeRect(0,0,600,76)] autorelease];
    self.detail.editable=NO;self.detail.selectable=YES;self.detail.font=[NSFont monospacedSystemFontOfSize:11 weight:NSFontWeightRegular];self.detail.textColor=NSColor.labelColor;
    self.detail.textContainerInset=NSMakeSize(8,6);self.detail.autoresizingMask=NSViewWidthSizable;self.detail.verticallyResizable=YES;self.detail.horizontallyResizable=NO;
    self.detail.textContainer.widthTracksTextView=YES;self.detail.string=@"Select an event to view its details.";
    [self.detail setAccessibilityLabel:@"Event details"];inspector.documentView=self.detail;
    [inspector.heightAnchor constraintEqualToConstant:82].active=YES;
    NSStackView *console=JaseStack(@[self.statistics,toolbar,scroll,inspector],NSUserInterfaceLayoutOrientationVertical);
    for(NSView *child in @[toolbar,scroll,inspector]) [child.widthAnchor constraintEqualToAnchor:console.widthAnchor].active=YES;
    NSStackView *content=JaseStack(@[siteScroll,console],NSUserInterfaceLayoutOrientationHorizontal);content.alignment=NSLayoutAttributeTop;
    [siteScroll.heightAnchor constraintEqualToAnchor:content.heightAnchor].active=YES;[console.heightAnchor constraintEqualToAnchor:content.heightAnchor].active=YES;
    NSView *pane=[[[NSView alloc] init] autorelease];JasePin(content,pane,14);
    return pane;
}
- (void)applicationDidFinishLaunching:(NSNotification *)note {
    [self setupInterface];
}
- (void)setupInterface {
    if(self.window)return;
    NSApp.applicationIconImage=JaseIcon(256,NO);
    self.events=[NSMutableArray array];self.models=@[];self.filteredEvents=@[];self.firstState=YES;self.sites=@[];self.selectedSite=@"*";
    self.dates=[[[NSISO8601DateFormatter alloc] init] autorelease];self.dates.formatOptions=NSISO8601DateFormatWithInternetDateTime|NSISO8601DateFormatWithFractionalSeconds;
    self.clock=[[[NSDateFormatter alloc] init] autorelease];self.clock.dateFormat=@"HH:mm:ss";
    self.statusItem=[NSStatusBar.systemStatusBar statusItemWithLength:NSSquareStatusItemLength];
    self.statusItem.button.image=JaseIcon(20,YES);self.statusItem.button.toolTip=@"Jase Tunnel";
    [self.statusItem.button setAccessibilityLabel:@"Jase Tunnel"];
    // Both mouse buttons open the standard compact macOS menu, with three items.
    NSMenu *menu=[[[NSMenu alloc] initWithTitle:@"Jase Tunnel"] autorelease];menu.autoenablesItems=NO;
    NSMenuItem *open=[menu addItemWithTitle:@"Open Jase Tunnel" action:@selector(showPanel:) keyEquivalent:@""];open.target=self;
    self.addMenuItem=[menu addItemWithTitle:@"Add weights" action:@selector(addWeights:) keyEquivalent:@""];self.addMenuItem.target=self;
    NSMenuItem *quit=[menu addItemWithTitle:@"Quit Jase Tunnel" action:@selector(quit:) keyEquivalent:@""];quit.target=self;
    self.statusItem.menu=menu;

    NSMenu *main=[[[NSMenu alloc] init] autorelease];NSMenuItem *root=[[[NSMenuItem alloc] init] autorelease];[main addItem:root];
    NSMenu *app=[[[NSMenu alloc] initWithTitle:@"Jase Tunnel"] autorelease];root.submenu=app;
    NSMenuItem *panel=[app addItemWithTitle:@"Open Jase Tunnel" action:@selector(showPanel:) keyEquivalent:@","];panel.target=self;
    NSMenuItem *add=[app addItemWithTitle:@"Add weights…" action:@selector(addWeights:) keyEquivalent:@"o"];add.target=self;
    [app addItem:NSMenuItem.separatorItem];[app addItemWithTitle:@"Nascondi Jase Tunnel" action:@selector(hide:) keyEquivalent:@"h"];
    [app addItem:NSMenuItem.separatorItem];NSMenuItem *exit=[app addItemWithTitle:@"Quit Jase Tunnel" action:@selector(quit:) keyEquivalent:@"q"];exit.target=self;
    NSMenuItem *editRoot=[[[NSMenuItem alloc] init] autorelease];[main addItem:editRoot];NSMenu *edit=[[[NSMenu alloc] initWithTitle:@"Edit"] autorelease];editRoot.submenu=edit;
    [edit addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];[edit addItemWithTitle:@"Select all" action:@selector(selectAll:) keyEquivalent:@"a"];
    NSMenuItem *windowRoot=[[[NSMenuItem alloc] init] autorelease];[main addItem:windowRoot];NSMenu *windows=[[[NSMenu alloc] initWithTitle:@"Window"] autorelease];windowRoot.submenu=windows;
    [windows addItemWithTitle:@"Close window" action:@selector(performClose:) keyEquivalent:@"w"];NSApp.mainMenu=main;NSApp.windowsMenu=windows;

    self.window=[[[NSWindow alloc] initWithContentRect:NSMakeRect(0,0,760,700) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable|NSWindowStyleMaskMiniaturizable|NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO] autorelease];
    self.window.title=@"Jase Tunnel";self.window.subtitle=@"Your local LLM, on websites that support it";self.window.minSize=NSMakeSize(680,640);
    self.window.releasedWhenClosed=NO;self.window.delegate=self;[self.window center];
    self.statusIcon=[NSImageView imageViewWithImage:[NSImage imageWithSystemSymbolName:@"network" accessibilityDescription:@"Connection"]];
    [self.statusIcon.widthAnchor constraintEqualToConstant:28].active=YES;[self.statusIcon.heightAnchor constraintEqualToConstant:28].active=YES;
    self.statusTitle=JaseLabel(@"Jase Tunnel is running",15,NSFontWeightSemibold);
    self.statusDetail=JaseLabel(@"Browse as usual: you will be notified when a page supports the tunnel",11,NSFontWeightRegular);self.statusDetail.textColor=NSColor.secondaryLabelColor;
    NSStackView *status=JaseStack(@[self.statusTitle,self.statusDetail],NSUserInterfaceLayoutOrientationVertical);status.spacing=5;
    NSStackView *header=JaseStack(@[self.statusIcon,status,JaseSpacer()],NSUserInterfaceLayoutOrientationHorizontal);
    self.tabs=[[[NSTabView alloc] init] autorelease];self.tabs.tabViewType=NSTopTabsBezelBorder;
    NSTabViewItem *models=[[[NSTabViewItem alloc] initWithIdentifier:@"models"] autorelease];models.label=@"Models";models.view=[self modelPane];[self.tabs addTabViewItem:models];
    NSTabViewItem *activity=[[[NSTabViewItem alloc] initWithIdentifier:@"activity"] autorelease];activity.label=@"Activity";activity.view=[self activityPane];[self.tabs addTabViewItem:activity];
    [self.tabs.heightAnchor constraintGreaterThanOrEqualToConstant:300].active=YES;
    self.debugButton=[NSButton checkboxWithTitle:@"Detailed log, including text" target:self action:@selector(debugChanged:)];
    self.thinkingButton=[NSButton checkboxWithTitle:@"Model reasoning" target:self action:@selector(thinkingChanged:)];
    NSStackView *options=JaseStack(@[self.debugButton,self.thinkingButton,JaseSpacer(),[self button:@"Disconnect websites" action:@selector(revokeSites:)]],NSUserInterfaceLayoutOrientationHorizontal);
    self.privacy=[NSTextField wrappingLabelWithString:@"Private Mac log · Last 300 events. Closing the window keeps the tunnel running in the menu bar."];
    self.privacy.font=[NSFont systemFontOfSize:11];self.privacy.textColor=NSColor.secondaryLabelColor;
    self.version=JaseLabel(@"",10,NSFontWeightRegular);self.version.textColor=NSColor.tertiaryLabelColor;
    NSStackView *footer=JaseStack(@[self.version,JaseSpacer(),[self button:@"Quit Jase Tunnel" action:@selector(quit:)]],NSUserInterfaceLayoutOrientationHorizontal);
    NSStackView *content=JaseStack(@[header,self.tabs,options,self.privacy,footer],NSUserInterfaceLayoutOrientationVertical);content.spacing=14;
    JasePin(content,self.window.contentView,20);
    for(NSView *child in @[header,self.tabs,options,self.privacy,footer]) [child.widthAnchor constraintEqualToAnchor:content.widthAnchor].active=YES;
    // Clicks on earlier notifications reach the app only once this delegate is set.
    if(NSBundle.mainBundle.bundleIdentifier)UNUserNotificationCenter.currentNotificationCenter.delegate=self;
    [self refresh:nil];self.timer=[NSTimer scheduledTimerWithTimeInterval:1.5 target:self selector:@selector(refresh:) userInfo:nil repeats:YES];
    [self showPanel:nil];
}
- (void)showPanel:(id)sender { [self.window makeKeyAndOrderFront:nil];[NSApp activateIgnoringOtherApps:YES]; }
// Announcements also appear while the app is in front; a click (or «Connect») lets
// the waiting page continue to the consent dialog. Dismissing it does nothing.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center willPresentNotification:(UNNotification *)notification withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
    completionHandler(UNNotificationPresentationOptionBanner|UNNotificationPresentationOptionList);
}
- (void)userNotificationCenter:(UNUserNotificationCenter *)center didReceiveNotificationResponse:(UNNotificationResponse *)response withCompletionHandler:(void (^)(void))completionHandler {
    NSString *key=response.notification.request.content.userInfo[@"discovery"];
    if([key isKindOfClass:NSString.class]&&![response.actionIdentifier isEqual:UNNotificationDismissActionIdentifier])jaseDesktopNotificationClicked((char *)key.UTF8String);
    completionHandler();
}
- (void)revokeSites:(id)sender { jaseDesktopAction("revoke-sites",0); }
- (void)revokeSite:(id)sender { if(![self.selectedSite isEqual:@"*"]&&self.selectedSite.length)jaseDesktopRevokeSite((char *)self.selectedSite.UTF8String); }
- (NSDictionary *)siteNamed:(NSString *)key { for(NSDictionary *site in self.sites)if([site[@"key"] isEqual:key])return site;return nil; }
- (void)showSite:(NSString *)key {
    [self.tabs selectTabViewItemWithIdentifier:@"activity"];self.selectedSite=key;
    NSUInteger row=[self.sites indexOfObjectPassingTest:^BOOL(NSDictionary *s,NSUInteger i,BOOL *stop){return [s[@"key"] isEqual:key];}];
    if(row!=NSNotFound)[self.siteTable selectRowIndexes:[NSIndexSet indexSetWithIndex:row] byExtendingSelection:NO];
    [self reloadEvents];
}
- (void)quit:(id)sender { jaseDesktopQuit(); }
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender { jaseDesktopQuit();return NSTerminateCancel; }
- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)sender { return NO; }
- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)visible { [self showPanel:nil];return YES; }
- (void)addWeights:(id)sender {
    if(self.importing)return;[self showPanel:nil];[self.tabs selectTabViewItemWithIdentifier:@"models"];
    NSOpenPanel *picker=[NSOpenPanel openPanel];picker.title=@"Add weights to Jase Tunnel";
    picker.message=@"Choose a GGUF file or an Ollama-compatible weights directory.";picker.prompt=@"Add weights";
    picker.canChooseFiles=YES;picker.canChooseDirectories=YES;picker.allowsMultipleSelection=NO;
    [picker beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse response){
        if(response!=NSModalResponseOK)return;NSURL *url=picker.URL;NSNumber *directory=nil;
        [url getResourceValue:&directory forKey:NSURLIsDirectoryKey error:nil];
        [self beginImport];jaseDesktopImport((char *)url.path.UTF8String,directory.boolValue);
    }];
}
- (void)beginImport {
    self.importing=YES;self.addButton.enabled=NO;self.addMenuItem.enabled=NO;self.importButton.enabled=NO;
    [self.progress startAnimation:nil];self.notice.stringValue=@"Adding weights to the local engine…";
}
- (void)importSelected:(id)sender {
    NSInteger row=self.modelTable.selectedRow;if(self.importing||row<0||row>=self.models.count)return;
    NSString *identifier=self.models[row][@"id"];if(!identifier)return;
    [self beginImport];jaseDesktopImportFound((char *)identifier.UTF8String);
}
- (void)finishImport:(NSString *)message {
    self.importing=NO;self.addButton.enabled=YES;self.addMenuItem.enabled=YES;[self.progress stopAnimation:nil];
    self.notice.stringValue=message;[self updateImportButton];[self refresh:nil];
}
- (void)updateImportButton {
    NSInteger row=self.modelTable.selectedRow;
    self.importButton.enabled=!self.importing&&row>=0&&row<self.models.count&&self.models[row][@"id"]!=nil;
}
- (void)debugChanged:(id)sender { jaseDesktopAction("debug",self.debugButton.state==NSControlStateValueOn);[self refresh:nil]; }
- (void)thinkingChanged:(id)sender { jaseDesktopAction("thinking",self.thinkingButton.state==NSControlStateValueOn);[self refresh:nil]; }
- (void)clearLog:(id)sender { jaseDesktopAction("clear",0);[self refresh:nil]; }
- (void)filterChanged:(id)sender { [self reloadEvents]; }
- (void)reloadEvents {
    NSInteger selected=self.logTable.selectedRow;id selectedID=selected>=0&&selected<self.filteredEvents.count?self.filteredEvents[selected][@"id"]:nil;
    NSString *filter=self.filter.selectedItem.representedObject;NSMutableArray *list=[NSMutableArray array];
    for(NSDictionary *event in self.events){
        if(![self.selectedSite isEqual:@"*"]&&![(event[@"site"]?:@"") isEqual:self.selectedSite])continue;
        if([filter isEqual:@"all"]||[event[@"category"] isEqual:filter]||([filter isEqual:@"error"]&&[event[@"level"] isEqual:@"error"]))[list addObject:event];
    }
    self.revokeSiteButton.enabled=[[self siteNamed:self.selectedSite][@"state"] isEqual:@"open"];
    BOOL atBottom=NSMaxY(self.logTable.visibleRect)>=NSHeight(self.logTable.bounds)-32;
    self.filteredEvents=list;[self.logTable reloadData];
    NSUInteger found=selectedID?[list indexOfObjectPassingTest:^BOOL(NSDictionary *e,NSUInteger idx,BOOL *stop){return [e[@"id"] isEqual:selectedID];}]:NSNotFound;
    if(found!=NSNotFound)[self.logTable selectRowIndexes:[NSIndexSet indexSetWithIndex:found] byExtendingSelection:NO];
    else self.detail.string=@"Select an event to view its details.";
    if(atBottom&&list.count)[self.logTable scrollRowToVisible:list.count-1];
}
- (void)refresh:(id)sender {
    if(!self.consenting&&!self.importing){
        char *rawApproval=jaseDesktopApproval();NSData *approvalData=[[NSString stringWithUTF8String:rawApproval] dataUsingEncoding:NSUTF8StringEncoding];free(rawApproval);
        id approval=[NSJSONSerialization JSONObjectWithData:approvalData options:NSJSONReadingFragmentsAllowed error:nil];
        if([approval isKindOfClass:NSDictionary.class]){
            self.consenting=YES;[self showPanel:nil];[self showSite:approval[@"origin"]];NSAlert *alert=[[[NSAlert alloc] init] autorelease];alert.messageText=[NSString stringWithFormat:@"Connect %@ to your model?",approval[@"site"]];
            alert.informativeText=approval[@"text"];
            NSPopUpButton *models=[[[NSPopUpButton alloc] initWithFrame:NSMakeRect(0,0,360,30) pullsDown:NO] autorelease];
            for(NSDictionary *model in approval[@"models"])[models addItemWithTitle:model[@"name"]];alert.accessoryView=models;
            NSButton *allow=[alert addButtonWithTitle:@"Allow this model"];allow.enabled=[approval[@"models"] count]>0;[alert addButtonWithTitle:@"Decline"];
            if(![approval[@"models"] count])alert.informativeText=[alert.informativeText stringByAppendingString:@"\n\nStart a compatible model in Ollama or LM Studio, then try again from the page."];
            [alert beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse answer){
                NSString *key=@"";if(answer==NSAlertFirstButtonReturn&&models.indexOfSelectedItem>=0)key=approval[@"models"][models.indexOfSelectedItem][@"key"];
                jaseDesktopConsent((char *)[approval[@"id"] UTF8String],(char *)key.UTF8String);self.consenting=NO;
            }];
        }
    }
    self.statusItem.button.image=JaseIcon(20,YES);
    char *raw=jaseDesktopState(self.cursor);if(!raw)return;
    NSData *data=[[NSString stringWithUTF8String:raw] dataUsingEncoding:NSUTF8StringEncoding];free(raw);
    NSDictionary *state=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];if(!state[@"models"])return;
    NSMutableArray *models=[NSMutableArray array];
    for(NSDictionary *m in state[@"models"])[models addObject:@{@"name":m[@"name"],@"engine":[m[@"engine"] isEqual:@"ollama"]?@"Ollama":@"LM Studio",@"status":[m[@"tools"] boolValue]?@"Ready":@"Tools unavailable"}];
    for(NSDictionary *f in state[@"files"])[models addObject:@{@"id":f[@"id"],@"name":f[@"name"],@"engine":f[@"format"],@"status":@"Weights to add"}];
    if(![self.models isEqual:models]){self.models=models;[self.modelTable reloadData];[self updateImportButton];}
    BOOL reset=self.firstState||self.generation!=[state[@"generation"] unsignedLongLongValue];
    if(reset){[self.events removeAllObjects];self.generation=[state[@"generation"] unsignedLongLongValue];self.firstState=NO;}
    [self.events addObjectsFromArray:state[@"events"]];if(self.events.count>300)[self.events removeObjectsInRange:NSMakeRange(0,self.events.count-300)];
    self.cursor=[state[@"cursor"] unsignedLongLongValue];if(reset||[state[@"events"] count])[self reloadEvents];
    NSMutableArray *open=[NSMutableArray array],*sites=[NSMutableArray arrayWithArray:@[@{@"key":@"*",@"label":@"All activity",@"status":@"App and websites"},@{@"key":@"",@"label":@"Jase Tunnel",@"status":@"Startup, models and settings"}]];
    NSInteger waiting=0;
    for(NSDictionary *site in state[@"sites"]){
        [sites addObject:@{@"key":site[@"origin"],@"label":site[@"label"],@"status":site[@"status"],@"state":site[@"state"]}];
        if([site[@"state"] isEqual:@"open"])[open addObject:[NSString stringWithFormat:@"%@ (%@)",site[@"label"],site[@"model"]?:@""]];
        if([site[@"state"] isEqual:@"awaiting_approval"])waiting++;
    }
    if(![self.sites isEqual:sites]){
        self.sites=sites;[self.siteTable reloadData];
        if(![self siteNamed:self.selectedSite])self.selectedSite=@"*";
        NSUInteger row=[sites indexOfObjectPassingTest:^BOOL(NSDictionary *s,NSUInteger i,BOOL *stop){return [s[@"key"] isEqual:self.selectedSite];}];
        [self.siteTable selectRowIndexes:[NSIndexSet indexSetWithIndex:row==NSNotFound?0:row] byExtendingSelection:NO];
        [self reloadEvents];
    }
    NSString *title=open.count==0?@"Jase Tunnel is running":open.count==1?@"1 open tunnel":[NSString stringWithFormat:@"%lu open tunnels",(unsigned long)open.count];
    if(waiting==1)title=[title stringByAppendingString:@" · a website is requesting a tunnel"];
    else if(waiting>1)title=[title stringByAppendingFormat:@" · %ld websites are requesting a tunnel",(long)waiting];
    self.statusTitle.stringValue=title;
    self.statusDetail.stringValue=open.count?[open componentsJoinedByString:@"   ·   "]:@"Browse as usual: you will be notified when a page supports the tunnel";
    self.statusIcon.contentTintColor=open.count?NSColor.systemGreenColor:NSColor.secondaryLabelColor;
    NSDictionary *stats=state[@"statistics"];
    self.statistics.stringValue=[NSString stringWithFormat:@"%@ sent   ·   %@ received   ·   %@ model turns   ·   %@ errors",stats[@"sent"],stats[@"received"],stats[@"modelTurns"],stats[@"errors"]];
    self.debugButton.state=[state[@"debug"] boolValue]?NSControlStateValueOn:NSControlStateValueOff;
    self.thinkingButton.state=[state[@"thinking"] boolValue]?NSControlStateValueOn:NSControlStateValueOff;
    self.privacy.stringValue=[state[@"debug"] boolValue]?@"Text stays on this Mac and may contain personal data. Turning off logging clears the view; existing log files remain on disk.":@"Private Mac log · Last 300 events. Closing the window keeps the tunnel running in the menu bar.";
    self.version.stringValue=[@"Jase Tunnel · " stringByAppendingString:state[@"version"]];
}
- (NSInteger)numberOfRowsInTableView:(NSTableView *)table { return table==self.modelTable?self.models.count:table==self.siteTable?self.sites.count:self.filteredEvents.count; }
- (NSView *)tableView:(NSTableView *)table viewForTableColumn:(NSTableColumn *)column row:(NSInteger)row {
    NSTextField *label=[table makeViewWithIdentifier:column.identifier owner:self];
    if(!label){label=JaseLabel(@"",12,NSFontWeightRegular);label.identifier=column.identifier;label.selectable=NO;}
    label.textColor=NSColor.labelColor;
    if(table==self.modelTable){label.stringValue=self.models[row][column.identifier]?:@"";return label;}
    if(table==self.siteTable){
        NSDictionary *site=self.sites[row];BOOL open=[site[@"state"] isEqual:@"open"],asking=[site[@"state"] isEqual:@"awaiting_approval"];
        NSMutableAttributedString *text=[[[NSMutableAttributedString alloc] initWithString:[NSString stringWithFormat:@"%@%@\n",open?@"● ":asking?@"◐ ":@"",site[@"label"]] attributes:@{NSFontAttributeName:[NSFont systemFontOfSize:12 weight:NSFontWeightSemibold],NSForegroundColorAttributeName:open?NSColor.systemGreenColor:asking?NSColor.systemOrangeColor:NSColor.labelColor}] autorelease];
        [text appendAttributedString:[[[NSAttributedString alloc] initWithString:site[@"status"]?:@"" attributes:@{NSFontAttributeName:[NSFont systemFontOfSize:10],NSForegroundColorAttributeName:NSColor.secondaryLabelColor}] autorelease]];
        label.usesSingleLineMode=NO;label.cell.wraps=YES;label.cell.truncatesLastVisibleLine=YES;label.maximumNumberOfLines=2;label.attributedStringValue=text;label.toolTip=[NSString stringWithFormat:@"%@\n%@",site[@"key"],site[@"status"]?:@""];return label;
    }
    NSDictionary *event=self.filteredEvents[row];
    if([column.identifier isEqual:@"time"]){NSDate *date=[self.dates dateFromString:event[@"time"]];label.stringValue=date?[self.clock stringFromDate:date]:@"";label.textColor=NSColor.secondaryLabelColor;}
    else {
        NSString *symbol=@{@"server":@"↑",@"receive":@"↓",@"model":@"◇",@"app":@"•"}[event[@"category"]]?:@"•";
        label.stringValue=[NSString stringWithFormat:@"%@  %@",symbol,event[@"title"]];
        if([event[@"level"] isEqual:@"error"])label.textColor=NSColor.systemRedColor;
    }
    label.toolTip=label.stringValue;return label;
}
- (void)tableViewSelectionDidChange:(NSNotification *)notification {
    if(notification.object==self.modelTable){[self updateImportButton];return;}
    if(notification.object==self.siteTable){
        NSInteger row=self.siteTable.selectedRow;if(row<0||row>=self.sites.count)return;
        NSString *key=self.sites[row][@"key"];if([key isEqual:self.selectedSite])return;
        self.selectedSite=key;[self reloadEvents];return;
    }
    NSInteger row=self.logTable.selectedRow;if(row<0||row>=self.filteredEvents.count)return;
    NSDictionary *event=self.filteredEvents[row];self.detail.string=[NSString stringWithFormat:@"%@\n%@",event[@"title"],event[@"detail"]?:@"No additional details."];
}
@end

static JaseDesktopDelegate *desktop;
void JaseRunDesktop(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        desktop=[JaseDesktopDelegate new];NSApp.delegate=desktop;
        // A replacement alert may already have completed application launching
        // before this delegate existed. Set up once in both startup paths.
        [desktop setupInterface];[NSApp run];
        [desktop.timer invalidate];[NSStatusBar.systemStatusBar removeStatusItem:desktop.statusItem];[desktop.window orderOut:nil];NSApp.delegate=nil;
        [desktop release];desktop=nil;
    }
}
void JaseShowDesktop(void) { dispatch_async(dispatch_get_main_queue(),^{[desktop showPanel:nil];}); }
void JaseImportFinished(const char *message) {
    NSString *text=[NSString stringWithUTF8String:message];dispatch_async(dispatch_get_main_queue(),^{[desktop finishImport:text];});
}
void JaseStopDesktop(void) {
    dispatch_async(dispatch_get_main_queue(),^{
        [desktop.timer invalidate];[NSApp stop:nil];
        [NSApp postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil subtype:0 data1:0 data2:0] atStart:YES];
    });
}
int JaseConfirmReplacement(void) {
    @autoreleasepool {
        [NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];[NSApp activateIgnoringOtherApps:YES];
        NSAlert *alert=[[[NSAlert alloc] init] autorelease];alert.messageText=@"An earlier version of Jase Tunnel is already running";
        alert.informativeText=@"Restart it to use the new panel. Current requests will be interrupted.";
        [alert addButtonWithTitle:@"Restart Jase Tunnel"];[alert addButtonWithTitle:@"Cancel"];return [alert runModal]==NSAlertFirstButtonReturn;
    }
}
void JaseStartupError(const char *message) {
    @autoreleasepool {[NSApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];[NSApp activateIgnoringOtherApps:YES];NSAlert *alert=[[[NSAlert alloc] init] autorelease];alert.messageText=@"Jase Tunnel could not start";alert.informativeText=[NSString stringWithUTF8String:message];[alert runModal];}
}
// Notification Center requires the app bundle. Permission is asked on the first
// announcement; if it is refused, the announcement stays in the panel's journal.
void JaseNotify(const char *identifier,const char *title,const char *body) {
    @autoreleasepool {
        NSString *key=[NSString stringWithUTF8String:identifier],*heading=[NSString stringWithUTF8String:title],*text=[NSString stringWithUTF8String:body];
        dispatch_async(dispatch_get_main_queue(),^{
            if(!NSBundle.mainBundle.bundleIdentifier){NSLog(@"Jase Tunnel: notifications need the app bundle");return;}
            UNUserNotificationCenter *center=UNUserNotificationCenter.currentNotificationCenter;
            if(desktop)center.delegate=desktop;
            UNNotificationAction *connect=[UNNotificationAction actionWithIdentifier:@"connect" title:@"Connect" options:UNNotificationActionOptionForeground];
            [center setNotificationCategories:[NSSet setWithObject:[UNNotificationCategory categoryWithIdentifier:@"compatible-site" actions:@[connect] intentIdentifiers:@[] options:0]]];
            [center requestAuthorizationWithOptions:UNAuthorizationOptionAlert|UNAuthorizationOptionSound completionHandler:^(BOOL granted,NSError *error){
                NSLog(@"Jase Tunnel: notification authorization %@ %@",granted?@"granted":@"refused",error?:@"");
                if(!granted)return;
                UNMutableNotificationContent *content=[[[UNMutableNotificationContent alloc] init] autorelease];
                content.title=heading;content.body=text;content.categoryIdentifier=@"compatible-site";content.userInfo=@{@"discovery":key};
                [center addNotificationRequest:[UNNotificationRequest requestWithIdentifier:key content:content trigger:nil] withCompletionHandler:^(NSError *failure){NSLog(@"Jase Tunnel: notification %@",failure?:@"delivered");}];
            }];
        });
    }
}
