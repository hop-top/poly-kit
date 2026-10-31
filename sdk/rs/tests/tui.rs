//! Public-API smoke for the `tui` feature: everything runs on injected
//! readers and writers, no terminal needed.
#![cfg(feature = "tui")]

use hop_top_kit::tui::progress::{self, Event, ProgressFormat};
use hop_top_kit::tui::{
    status_line, AppRenderer, AppShell, Bar, Cmd, ColorMode, Confirm, InfoType, Msg, Styler, Theme,
};
use std::io::Write;
use std::sync::{Arc, Mutex};

#[derive(Clone, Default)]
struct Sink(Arc<Mutex<Vec<u8>>>);

impl Write for Sink {
    fn write(&mut self, b: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap().extend_from_slice(b);
        Ok(b.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

struct App;
impl AppRenderer for App {
    fn render(&self, _: usize, _: usize) -> String {
        "body".into()
    }
}

#[test]
fn no_color_env_gate_is_honoured_by_resolve() {
    let off = Styler::resolve(Theme::default(), ColorMode::Always, true, true, false);
    assert_eq!(status_line(&off, InfoType::Success, "ok"), "✓ ok");
    assert!(!Bar::new(off).set_percent(0.5).view().contains('\x1b'));
}

#[test]
fn progress_selected_for_json_data_is_jsonl_on_the_injected_writer() {
    let s = Sink::default();
    progress::select(false, None::<ProgressFormat>, true, s.clone())
        .emit(Event::new("verify").ok(true));
    let text = String::from_utf8(s.0.lock().unwrap().clone()).unwrap();
    assert!(text.starts_with("{\"phase\":\"verify\",\"at\":\""));
}

#[test]
fn confirm_and_shell_drive_without_a_terminal() {
    let c = Confirm::new("Go?", Styler::plain());
    let mut out = Vec::new();
    assert!(!c
        .clone()
        .default_answer(false)
        .ask(&mut &b""[..], &mut out, false)
        .unwrap());
    assert!(c.ask(&mut &b"y\n"[..], &mut out, true).unwrap());

    let mut shell = AppShell::new(App, Styler::plain()).header("h");
    assert_eq!(shell.update(Msg::Key("q".into())), Cmd::Quit);
    assert!(shell.view().contains("body"));
}
