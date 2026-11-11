// HomeOwl's macOS menubar item: a status icon whose popover shows HomeOwl's /menubar.html page.
int hoGUIAvailable(void);
void hoInit(const char *url, int show);
void hoRun(void);
void hoSetVisible(int show);
void hoSetIcon(const void *png, int len, int tmpl, int dot, int level, const char *title);
void hoClosePopover(void);
void hoSetHeight(int h);
