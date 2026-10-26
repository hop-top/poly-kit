//! The shell directive bit set and what file completers ask for.

use std::fmt;
use std::ops::BitOr;

/// How a shell should treat the completions it was given. A bit set
/// with cobra's values, which the scripts read from the `:<n>` line.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct Directive(u32);

impl Directive {
    /// Let the shell perform its default behaviour.
    pub const DEFAULT: Directive = Directive(0);
    /// An error occurred; ignore the completions.
    pub const ERROR: Directive = Directive(1);
    /// Do not add a space after a single completion.
    pub const NO_SPACE: Directive = Directive(2);
    /// Do not fall back to file completion.
    pub const NO_FILE_COMP: Directive = Directive(4);
    /// The completions are file extensions to filter by.
    pub const FILTER_FILE_EXT: Directive = Directive(8);
    /// Complete directory names only.
    pub const FILTER_DIRS: Directive = Directive(16);
    /// Keep the order the completions were given in.
    pub const KEEP_ORDER: Directive = Directive(32);
    const MAX: u32 = 64;

    /// The raw bit set.
    pub fn bits(self) -> u32 {
        self.0
    }

    /// Whether every bit of `other` is set.
    pub fn contains(self, other: Directive) -> bool {
        self.0 & other.0 == other.0
    }
}

impl BitOr for Directive {
    type Output = Directive;
    fn bitor(self, rhs: Directive) -> Directive {
        Directive(self.0 | rhs.0)
    }
}

/// cobra's directive names, as printed on stderr after a request.
impl fmt::Display for Directive {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.0 >= Self::MAX {
            return write!(f, "ERROR: unexpected ShellCompDirective value: {}", self.0);
        }
        let names = [
            (Self::ERROR, "ShellCompDirectiveError"),
            (Self::NO_SPACE, "ShellCompDirectiveNoSpace"),
            (Self::NO_FILE_COMP, "ShellCompDirectiveNoFileComp"),
            (Self::FILTER_FILE_EXT, "ShellCompDirectiveFilterFileExt"),
            (Self::FILTER_DIRS, "ShellCompDirectiveFilterDirs"),
            (Self::KEEP_ORDER, "ShellCompDirectiveKeepOrder"),
        ];
        let set: Vec<&str> = names
            .iter()
            .filter(|(d, _)| self.contains(*d))
            .map(|(_, n)| *n)
            .collect();
        if set.is_empty() {
            f.write_str("ShellCompDirectiveDefault")
        } else {
            f.write_str(&set.join(", "))
        }
    }
}

/// What a file completer asks the shell for.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum FileFilter {
    /// Files with these extensions; empty means any file.
    Extensions(Vec<String>),
    /// Directories only.
    Dirs,
}
