//! Help rendering: the layout the TypeScript and Python kits print
//! (`Usage:` line, description, `ARGUMENTS:`, `COMMANDS:` and one
//! section per help group, `FLAGS:` last), built structurally from the
//! kit [`CommandInfo`] model, and the [`Theme`] that colors it.
//!
//! Go delegates to fang; this renderer reproduces the layout contract
//! (`contracts/parity/parity.json`, `help`) without regex post-processing:
//! the plain and the styled output come from the same pass, the styled
//! one only wrapping terms in SGR sequences.

use std::borrow::Cow;
use std::io::IsTerminal;

use super::model::{CommandInfo, FlagInfo, FlagKind};

/// The default command section title.
const COMMANDS_TITLE: &str = "COMMANDS";
const FLAGS_TITLE: &str = "FLAGS";
const ARGUMENTS_TITLE: &str = "ARGUMENTS";

/// The two brand colors of a theme (Go `cli.Palette`), as `#RRGGBB`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Palette {
    /// Commands and the primary accent.
    pub command: Cow<'static, str>,
    /// Flags and the secondary accent.
    pub flag: Cow<'static, str>,
}

impl Palette {
    /// Vivid: grass green and neon pink.
    pub const NEON: Palette = Palette::fixed("#7ED957", "#FF00FF");
    /// Softer: lime and pink.
    pub const DARK: Palette = Palette::fixed("#C1FF72", "#FF66C4");
    /// High-contrast primaries: magenta and blue.
    pub const BAUHAUS: Palette = Palette::fixed("#FF00AA", "#0000FF");

    const fn fixed(command: &'static str, flag: &'static str) -> Palette {
        Palette {
            command: Cow::Borrowed(command),
            flag: Cow::Borrowed(flag),
        }
    }
}

/// Semantic colors for help output (Go `cli.Theme`), as `#RRGGBB`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Theme {
    /// The brand colors.
    pub palette: Palette,
    /// Primary accent: command names, the usage program name.
    pub accent: String,
    /// Secondary accent: flag names.
    pub secondary: String,
    /// Muted text (CharmTone Squid).
    pub muted: String,
    /// Errors.
    pub error: String,
    /// Success (CharmTone Guac).
    pub success: String,
    /// Section titles.
    pub title: String,
    /// Argument placeholders.
    pub argument: String,
    /// Dimmed argument placeholders.
    pub dimmed_argument: String,
}

impl Theme {
    /// The Neon theme; a non-empty `accent` replaces the command color
    /// (Go `buildTheme`).
    pub fn new(accent: &str) -> Theme {
        let mut p = Palette::NEON;
        if !accent.is_empty() {
            p.command = Cow::Owned(accent.to_string());
        }
        Theme::from_palette(p)
    }

    /// A theme over `palette`.
    pub fn from_palette(palette: Palette) -> Theme {
        Theme {
            accent: palette.command.to_string(),
            secondary: palette.flag.to_string(),
            palette,
            muted: "#858183".into(),
            error: "#FF00AA".into(),
            success: "#52CF84".into(),
            title: "#FFFFFF".into(),
            argument: "#B5E89B".into(),
            dimmed_argument: "#8ABF6E".into(),
        }
    }
}

impl Default for Theme {
    fn default() -> Theme {
        Theme::new("")
    }
}

/// `#RRGGBB` as its SGR true-color foreground prefix; `None` when the
/// text is not a six-digit hex color.
fn sgr(hex: &str) -> Option<String> {
    let h = hex.strip_prefix('#')?;
    if h.len() != 6 || !h.is_ascii() {
        return None;
    }
    let c = |i: usize| u8::from_str_radix(&h[i..i + 2], 16).ok();
    Some(format!("\x1b[38;2;{};{};{}m", c(0)?, c(2)?, c(4)?))
}

/// Which part of the tree a help screen shows.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Scope<'a> {
    /// Visible groups and flags only.
    Default,
    /// Every group; with `plumbing`, the hidden flags too.
    All { plumbing: bool },
    /// Only the commands of one group.
    Group(&'a str),
}

/// A help group: id, section title, hidden from default help.
#[derive(Clone, Debug)]
pub(crate) struct GroupDef {
    pub id: String,
    pub title: String,
    pub hidden: bool,
}

/// One help screen.
pub(crate) struct Help<'a> {
    pub root_name: &'a str,
    pub target: &'a CommandInfo,
    pub scope: Scope<'a>,
    pub groups: &'a [GroupDef],
    pub theme: Option<&'a Theme>,
}

#[derive(Clone, Copy)]
enum Kind {
    Title,
    Command,
    Flag,
    Argument,
}

struct Item {
    term: String,
    kind: Kind,
    desc: String,
}

impl Help<'_> {
    fn paint(&self, kind: Kind, s: &str) -> String {
        let Some(t) = self.theme else {
            return s.to_string();
        };
        let (color, bold) = match kind {
            Kind::Title => (&t.title, true),
            Kind::Command => (&t.accent, false),
            Kind::Flag => (&t.secondary, false),
            Kind::Argument => (&t.argument, false),
        };
        let Some(c) = sgr(color) else {
            return s.to_string();
        };
        let b = if bold { "\x1b[1m" } else { "" };
        format!("{b}{c}{s}\x1b[0m")
    }

    /// The help text.
    pub fn render(&self) -> String {
        let t = self.target;
        let mut sections: Vec<(String, Vec<Item>)> = Vec::new();

        let args: Vec<Item> = t
            .args
            .iter()
            .filter(|a| !a.usage.is_empty())
            .map(|a| Item {
                term: a.name.clone(),
                kind: Kind::Argument,
                desc: a.usage.clone(),
            })
            .collect();
        if !args.is_empty() {
            sections.push((ARGUMENTS_TITLE.into(), args));
        }

        for (title, cmds) in self.command_sections() {
            sections.push((
                title,
                cmds.into_iter()
                    .map(|c| Item {
                        term: command_term(c),
                        kind: Kind::Command,
                        desc: c.short.clone(),
                    })
                    .collect(),
            ));
        }

        let flags = self.flag_items();
        if !flags.is_empty() {
            sections.push((FLAGS_TITLE.into(), flags));
        }

        let width = sections
            .iter()
            .flat_map(|(_, items)| items.iter().map(|i| i.term.chars().count()))
            .max()
            .unwrap_or(0);

        let mut out = String::new();
        out.push_str(&format!(
            "{} {}\n\n",
            self.paint(Kind::Title, "Usage:"),
            self.usage_line()
        ));
        let desc = if t.long.is_empty() { &t.short } else { &t.long };
        if !desc.is_empty() {
            out.push_str(desc);
            out.push_str("\n\n");
        }
        for (title, items) in sections {
            out.push_str(&self.paint(Kind::Title, &format!("{title}:")));
            out.push('\n');
            for i in items {
                let pad = width - i.term.chars().count();
                out.push_str("  ");
                out.push_str(&self.paint(i.kind, &i.term));
                if !i.desc.is_empty() {
                    out.push_str(&" ".repeat(pad + 2));
                    out.push_str(&i.desc);
                }
                out.push('\n');
            }
            out.push('\n');
        }
        out
    }

    fn usage_line(&self) -> String {
        let t = self.target;
        let name = self.target_path();
        let mut line = self.paint(Kind::Command, &name);
        for a in &t.args {
            let dots = if a.variadic { "..." } else { "" };
            let term = if a.required {
                format!("<{}{dots}>", a.name)
            } else {
                format!("[{}{dots}]", a.name)
            };
            line.push(' ');
            line.push_str(&self.paint(Kind::Argument, &term));
        }
        if self.visible_flags().next().is_some() {
            line.push_str(" [flags]");
        }
        if t.commands.iter().any(|c| !c.hidden) {
            line.push_str(" [command]");
        }
        line
    }

    /// The root name followed by the command path.
    fn target_path(&self) -> String {
        let mut words = vec![self.root_name()];
        words.extend(self.target.path.iter().cloned());
        words.join(" ")
    }

    fn root_name(&self) -> String {
        self.root_name.to_string()
    }

    /// `(title, commands)` per section, in order: the default group,
    /// then the declared groups, then any group id met on a command.
    fn command_sections(&self) -> Vec<(String, Vec<&CommandInfo>)> {
        let mut out: Vec<(String, Vec<&CommandInfo>)> = Vec::new();
        let group_hidden = |id: &str| self.groups.iter().any(|g| g.id == id && g.hidden);
        let visible = |c: &&CommandInfo| !c.hidden;
        let ungrouped: Vec<&CommandInfo> = self
            .target
            .commands
            .iter()
            .filter(visible)
            .filter(|c| c.group.is_none())
            .collect();
        if !ungrouped.is_empty() && !matches!(self.scope, Scope::Group(_)) {
            out.push((COMMANDS_TITLE.into(), ungrouped));
        }
        let mut ids: Vec<String> = self.groups.iter().map(|g| g.id.clone()).collect();
        for c in &self.target.commands {
            if let Some(id) = &c.group {
                if !ids.contains(id) {
                    ids.push(id.clone());
                }
            }
        }
        for id in ids {
            match self.scope {
                Scope::Group(only) if only != id => continue,
                Scope::Default if group_hidden(&id) => continue,
                _ => {}
            }
            let cmds: Vec<&CommandInfo> = self
                .target
                .commands
                .iter()
                .filter(visible)
                .filter(|c| c.group.as_deref() == Some(id.as_str()))
                .collect();
            if cmds.is_empty() {
                continue;
            }
            let title = self
                .groups
                .iter()
                .find(|g| g.id == id)
                .map(|g| g.title.clone())
                .unwrap_or_else(|| id.to_uppercase());
            out.push((title, cmds));
        }
        out
    }

    fn visible_flags(&self) -> impl Iterator<Item = &FlagInfo> {
        let plumbing = matches!(self.scope, Scope::All { plumbing: true });
        self.target
            .flags
            .iter()
            .filter(move |f| (plumbing || !f.hidden) && f.name != "help")
    }

    fn flag_items(&self) -> Vec<Item> {
        let mut items = vec![Item {
            term: "-h, --help".into(),
            kind: Kind::Flag,
            desc: format!("Help for {}", self.target.name_or_root(&self.root_name())),
        }];
        let group_flags: Vec<&str> = self
            .visible_flags()
            .filter_map(|f| f.name.strip_prefix("help-"))
            .filter(|id| *id != "all")
            .collect();
        let mut folded = false;
        for f in self.visible_flags() {
            let is_group = f.name.starts_with("help-") && f.name != "help-all";
            if is_group && group_flags.len() >= 2 {
                if !folded {
                    folded = true;
                    let mut ids = group_flags.clone();
                    ids.sort_unstable();
                    items.push(Item {
                        term: "    --help-<group>".into(),
                        kind: Kind::Flag,
                        desc: format!("Show only one group's commands: {}", ids.join(", ")),
                    });
                }
                continue;
            }
            items.push(Item {
                term: flag_term(f),
                kind: Kind::Flag,
                desc: flag_desc(f),
            });
        }
        items
    }
}

impl CommandInfo {
    fn name_or_root(&self, root: &str) -> String {
        if self.path.is_empty() {
            root.to_string()
        } else {
            self.path.join(" ")
        }
    }
}

fn flag_term(f: &FlagInfo) -> String {
    let mut t = match f.short {
        Some(c) => format!("-{c}, --{}", f.name),
        None => format!("    --{}", f.name),
    };
    if f.kind.takes_value() {
        t.push_str(&format!(" <{}>", f.value_name));
    }
    t
}

fn flag_desc(f: &FlagInfo) -> String {
    let mut d = f.usage.clone();
    if let Some(def) = &f.default {
        if !def.is_empty() && !matches!(f.kind, FlagKind::Bool | FlagKind::Count) {
            d.push_str(&format!(" (default {def})"));
        }
    }
    d
}

/// `name <args> [command]` / `name <args> [--flags]` / `name`.
fn command_term(c: &CommandInfo) -> String {
    let mut t = c.name.clone();
    for a in &c.args {
        if a.required {
            t.push_str(&format!(" <{}>", a.name));
        } else {
            t.push_str(&format!(" [{}]", a.name));
        }
    }
    if c.commands.iter().any(|s| !s.hidden) {
        t.push_str(" [command]");
    } else if c.flags.iter().any(|f| !f.hidden && f.name != "help") {
        t.push_str(" [--flags]");
    }
    t
}

/// Whether help may be colored: stdout is a terminal, and neither
/// `NO_COLOR`, `TERM=dumb` nor `--no-color` asks for plain text.
pub(crate) fn color_enabled(args: &[String]) -> bool {
    if args
        .iter()
        .take_while(|a| *a != "--")
        .any(|a| a == "--no-color")
    {
        return false;
    }
    if std::env::var_os("NO_COLOR").is_some_and(|v| !v.is_empty()) {
        return false;
    }
    if std::env::var("TERM").is_ok_and(|t| t == "dumb") {
        return false;
    }
    std::io::stdout().is_terminal()
}
