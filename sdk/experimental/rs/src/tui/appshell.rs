//! Header / main / footer frame with the canonical keymap
//! (`tui.AppShell` in Go), as a pure state machine.
//!
//! Go's shell owns the bubbletea program loop. Without a widget framework
//! there is no loop to own here: the caller reads input and the terminal
//! size however it likes, feeds [`Msg`]s to [`AppShell::update`], stops on
//! [`Cmd::Quit`] and prints [`AppShell::view`]. The Go optional-interface
//! capabilities (`Initer`, `Updater`, `Resizer`, `HeaderRenderer`,
//! `FooterRenderer`) become default methods of one trait.

use super::style::Styler;

/// Rows the shell reserves above and below the main region.
const HEADER_HEIGHT: usize = 1;
const FOOTER_HEIGHT: usize = 1;

/// What the app renders. Only [`render`](Self::render) is required.
pub trait AppRenderer {
    /// Main region content for the given size.
    fn render(&self, width: usize, height: usize) -> String;

    /// Called once by [`AppShell::init`].
    fn init(&mut self) {}

    /// Called for every message the shell did not consume itself
    /// (resizes always reach it). The returned [`Cmd`] passes through.
    fn update(&mut self, _msg: &Msg) -> Cmd {
        Cmd::None
    }

    /// Called with the main-region size at construction and on resize.
    fn resize(&mut self, _width: usize, _height: usize) {}

    /// Overrides the static header when `Some`.
    fn header(&self, _width: usize) -> Option<String> {
        None
    }

    /// Overrides the static footer when `Some`.
    fn footer(&self, _width: usize) -> Option<String> {
        None
    }
}

/// Input to [`AppShell::update`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Msg {
    /// New terminal size.
    Resize { width: usize, height: usize },
    /// A key by name: `"q"`, `"esc"`, `"ctrl+c"`, `"?"`.
    Key(String),
}

/// What the caller's loop must do next.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Cmd {
    None,
    Quit,
}

/// Key names bound to each shell action.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct KeyMap {
    pub quit: Vec<String>,
    pub help: Vec<String>,
}

impl Default for KeyMap {
    /// `q`, `esc`, `ctrl+c` quit; `?`, `h` toggle help.
    fn default() -> Self {
        let v = |a: &[&str]| a.iter().map(|s| s.to_string()).collect();
        KeyMap {
            quit: v(&["q", "esc", "ctrl+c"]),
            help: v(&["?", "h"]),
        }
    }
}

/// The frame around an [`AppRenderer`].
pub struct AppShell<R: AppRenderer> {
    renderer: R,
    styler: Styler,
    keymap: KeyMap,
    width: usize,
    height: usize,
    header: String,
    footer: String,
    help_text: String,
    show_help: bool,
    alt_screen: bool,
}

impl<R: AppRenderer> AppShell<R> {
    /// 80x24, canonical keymap, empty header and footer, alt screen on.
    pub fn new(renderer: R, styler: Styler) -> Self {
        let mut a = AppShell {
            renderer,
            styler,
            keymap: KeyMap::default(),
            width: 80,
            height: 24,
            header: String::new(),
            footer: String::new(),
            help_text: "  q/esc quit  ?/h help".to_string(),
            show_help: false,
            alt_screen: true,
        };
        a.resize_renderer();
        a
    }

    pub fn keymap(mut self, km: KeyMap) -> Self {
        self.keymap = km;
        self
    }

    pub fn header(mut self, text: impl Into<String>) -> Self {
        self.header = text.into();
        self
    }

    pub fn footer(mut self, text: impl Into<String>) -> Self {
        self.footer = text.into();
        self
    }

    pub fn help_text(mut self, text: impl Into<String>) -> Self {
        self.help_text = text.into();
        self
    }

    /// Initial size, used until the first [`Msg::Resize`].
    pub fn size(mut self, width: usize, height: usize) -> Self {
        self.width = width;
        self.height = height;
        self.resize_renderer();
        self
    }

    /// Whether the caller should draw on the alternate screen. The shell
    /// does not switch screens itself.
    pub fn alt_screen(mut self, on: bool) -> Self {
        self.alt_screen = on;
        self
    }

    pub fn wants_alt_screen(&self) -> bool {
        self.alt_screen
    }

    pub fn width(&self) -> usize {
        self.width
    }

    pub fn height(&self) -> usize {
        self.height
    }

    /// Rows left for the main region.
    pub fn content_height(&self) -> usize {
        self.height.saturating_sub(HEADER_HEIGHT + FOOTER_HEIGHT)
    }

    pub fn renderer(&self) -> &R {
        &self.renderer
    }

    pub fn renderer_mut(&mut self) -> &mut R {
        &mut self.renderer
    }

    pub fn into_renderer(self) -> R {
        self.renderer
    }

    pub fn help_shown(&self) -> bool {
        self.show_help
    }

    /// Run the renderer's start-up hook.
    pub fn init(&mut self) {
        self.renderer.init();
    }

    /// Handle a message: a resize updates the size and reaches the
    /// renderer; a quit key returns [`Cmd::Quit`]; a help key toggles the
    /// help footer; everything else is the renderer's.
    pub fn update(&mut self, msg: Msg) -> Cmd {
        match &msg {
            Msg::Resize { width, height } => {
                self.width = *width;
                self.height = *height;
                self.resize_renderer();
            }
            Msg::Key(k) => {
                if self.keymap.quit.iter().any(|q| q == k) {
                    return Cmd::Quit;
                }
                if self.keymap.help.iter().any(|h| h == k) {
                    self.show_help = !self.show_help;
                    return Cmd::None;
                }
            }
        }
        self.renderer.update(&msg)
    }

    /// The frame: header row, main region, footer row, padded to the
    /// terminal size (`height` lines, each padded to `width` cells).
    /// Width counts chars outside ANSI escapes; wide glyphs are not
    /// measured and long lines are not wrapped.
    pub fn view(&self) -> String {
        let w = self.width;
        let ch = self.content_height();
        let header = self
            .renderer
            .header(w)
            .unwrap_or_else(|| self.header.clone());
        let footer = if self.show_help && !self.help_text.is_empty() {
            self.help_text.clone()
        } else {
            self.renderer
                .footer(w)
                .unwrap_or_else(|| self.footer.clone())
        };
        let main = self.renderer.render(w, ch);

        let mut lines: Vec<String> = Vec::with_capacity(self.height);
        if self.height > 0 {
            lines.push(pad(&self.styler.accent_bold(&first_line(&header)), w));
        }
        let mut body = main.lines().take(ch).map(|l| pad(l, w)).collect::<Vec<_>>();
        body.resize(ch, pad("", w));
        lines.extend(body);
        if self.height > HEADER_HEIGHT {
            lines.push(pad(&self.styler.muted(&first_line(&footer)), w));
        }
        lines.join("\n")
    }

    fn resize_renderer(&mut self) {
        let (w, h) = (self.width, self.content_height());
        self.renderer.resize(w, h);
    }
}

fn first_line(s: &str) -> String {
    s.lines().next().unwrap_or("").to_string()
}

/// Visible width: chars outside CSI escape sequences.
fn visible_width(s: &str) -> usize {
    let mut n = 0;
    let mut it = s.chars();
    while let Some(c) = it.next() {
        if c == '\x1b' {
            // CSI: ESC [ params final-byte(@..~)
            if it.next() == Some('[') {
                for c in it.by_ref() {
                    if ('@'..='~').contains(&c) {
                        break;
                    }
                }
            }
        } else {
            n += 1;
        }
    }
    n
}

fn pad(s: &str, width: usize) -> String {
    let v = visible_width(s);
    format!("{s}{}", " ".repeat(width.saturating_sub(v)))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tui::style::{ColorMode, Theme};

    #[derive(Default)]
    struct Fake {
        main: String,
        header: Option<String>,
        footer: Option<String>,
        inits: usize,
        resizes: Vec<(usize, usize)>,
        got: Vec<Msg>,
    }

    impl AppRenderer for Fake {
        fn render(&self, _w: usize, _h: usize) -> String {
            self.main.clone()
        }
        fn init(&mut self) {
            self.inits += 1;
        }
        fn update(&mut self, m: &Msg) -> Cmd {
            self.got.push(m.clone());
            Cmd::None
        }
        fn resize(&mut self, w: usize, h: usize) {
            self.resizes.push((w, h));
        }
        fn header(&self, _w: usize) -> Option<String> {
            self.header.clone()
        }
        fn footer(&self, _w: usize) -> Option<String> {
            self.footer.clone()
        }
    }

    fn fake(main: &str) -> Fake {
        Fake {
            main: main.into(),
            ..Default::default()
        }
    }

    fn shell(main: &str) -> AppShell<Fake> {
        AppShell::new(fake(main), Styler::plain())
    }

    fn key(k: &str) -> Msg {
        Msg::Key(k.into())
    }

    #[test]
    fn defaults() {
        let a = shell("x");
        assert_eq!((a.width(), a.height()), (80, 24));
        assert!(a.wants_alt_screen());
    }

    #[test]
    fn view_has_main_content() {
        assert!(shell("hello world").view().contains("hello world"));
    }

    #[test]
    fn header_and_footer() {
        let v = shell("m").header("HDR").footer("FTR").view();
        assert!(v.contains("HDR") && v.contains("FTR"));
    }

    #[test]
    fn renderer_header_footer_override_static() {
        let mut f = fake("M");
        f.header = Some("RENDERER-H".into());
        f.footer = Some("RENDERER-F".into());
        let v = AppShell::new(f, Styler::plain())
            .header("static-h")
            .footer("static-f")
            .view();
        assert!(v.contains("RENDERER-H") && v.contains("RENDERER-F"));
        assert!(!v.contains("static-h") && !v.contains("static-f"));
    }

    #[test]
    fn update_window_size() {
        let mut a = shell("x");
        assert_eq!(
            a.update(Msg::Resize {
                width: 120,
                height: 40
            }),
            Cmd::None
        );
        assert_eq!((a.width(), a.height()), (120, 40));
    }

    #[test]
    fn quit_keys() {
        for k in ["q", "esc", "ctrl+c"] {
            assert_eq!(shell("").update(key(k)), Cmd::Quit, "{k}");
        }
    }

    #[test]
    fn quit_is_not_forwarded() {
        let mut a = shell("");
        a.update(key("q"));
        assert!(a.renderer().got.is_empty());
    }

    #[test]
    fn help_toggle() {
        let mut a = shell("x").help_text("HELPLINE");
        a.update(key("?"));
        assert!(a.view().contains("HELPLINE"));
        a.update(key("?"));
        assert!(!a.view().contains("HELPLINE"));
        a.update(key("h"));
        assert!(a.help_shown());
    }

    #[test]
    fn help_preempts_renderer_footer() {
        let mut f = fake("m");
        f.footer = Some("RF".into());
        let mut a = AppShell::new(f, Styler::plain()).help_text("HELP");
        a.update(key("?"));
        let v = a.view();
        assert!(v.contains("HELP") && !v.contains("RF"));
    }

    #[test]
    fn delegates_other_keys_to_renderer() {
        let mut a = shell("X");
        a.update(key("x"));
        assert_eq!(a.renderer().got, vec![key("x")]);
    }

    #[test]
    fn init_delegates() {
        let mut a = shell("X");
        a.init();
        assert_eq!(a.renderer().inits, 1);
    }

    #[test]
    fn resize_on_construction() {
        let a = AppShell::new(fake("X"), Styler::plain()).size(100, 30);
        assert_eq!(a.renderer().resizes.last(), Some(&(100, 28)));
    }

    #[test]
    fn resize_on_window_size_msg() {
        let mut a = shell("X");
        a.update(Msg::Resize {
            width: 60,
            height: 20,
        });
        assert_eq!(a.renderer().resizes.last(), Some(&(60, 18)));
        assert_eq!(a.renderer().got.len(), 1, "resize also reaches update");
    }

    #[test]
    fn keymap_override() {
        let km = KeyMap {
            quit: vec!["x".into()],
            help: vec!["?".into()],
        };
        let mut a = shell("").keymap(km);
        assert_eq!(a.update(key("q")), Cmd::None);
        assert_eq!(a.update(key("x")), Cmd::Quit);
    }

    #[test]
    fn default_keymap_has_canonical_keys() {
        let km = KeyMap::default();
        for k in ["q", "esc", "ctrl+c"] {
            assert!(km.quit.iter().any(|x| x == k));
        }
        for k in ["?", "h"] {
            assert!(km.help.iter().any(|x| x == k));
        }
    }

    #[test]
    fn alt_screen_off() {
        assert!(!shell("x").alt_screen(false).wants_alt_screen());
    }

    #[test]
    fn view_pads_to_size() {
        let v = shell("tiny").size(40, 10).view();
        let lines: Vec<&str> = v.split('\n').collect();
        assert_eq!(lines.len(), 10);
        assert!(lines.iter().all(|l| l.chars().count() == 40));
    }

    #[test]
    fn view_clips_tall_main() {
        let v = shell("a\nb\nc\nd\ne").size(10, 5).view();
        assert_eq!(v.split('\n').count(), 5);
        assert!(v.contains('c') && !v.contains('d'));
    }

    #[test]
    fn content_height_floors_at_zero() {
        assert_eq!(shell("").size(10, 0).content_height(), 0);
        assert_eq!(shell("").size(10, 1).content_height(), 0);
        assert_eq!(shell("").size(10, 0).view(), "");
    }

    #[test]
    fn padding_ignores_ansi() {
        let on = Styler::resolve(Theme::default(), ColorMode::Always, false, false, false);
        let v = AppShell::new(fake("m"), on).header("HI").size(10, 3).view();
        for l in v.split('\n') {
            assert_eq!(visible_width(l), 10, "{l:?}");
        }
    }
}
