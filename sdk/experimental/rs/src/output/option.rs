//! OptionType, OptionSpec, Options + parse_options.
//!
//! Mirrors py output.formatter, ts output/formatter, go console/output,
//! and php Output/Formatter/{OptionType,OptionSpec,Options}.

use std::collections::HashMap;
use thiserror::Error;

/// Kinds of values an [`OptionSpec`] accepts.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OptionType {
    String,
    Int,
    Bool,
    Enum,
}

/// A validated option value produced by [`parse_options`].
#[derive(Debug, Clone, PartialEq)]
pub enum OptionValue {
    String(String),
    Int(i64),
    Bool(bool),
}

impl OptionValue {
    pub fn as_str(&self) -> Option<&str> {
        match self {
            Self::String(s) => Some(s),
            _ => None,
        }
    }
    pub fn as_int(&self) -> Option<i64> {
        if let Self::Int(n) = self {
            Some(*n)
        } else {
            None
        }
    }
    pub fn as_bool(&self) -> Option<bool> {
        if let Self::Bool(b) = self {
            Some(*b)
        } else {
            None
        }
    }
}

/// Validated map of option values, keyed by [`OptionSpec::name`].
pub type Options = HashMap<String, OptionValue>;

/// Describes one option accepted by a Formatter via `--format-opt key=value`.
#[derive(Debug, Clone)]
pub struct OptionSpec {
    pub name: &'static str,
    pub r#type: OptionType,
    pub usage: &'static str,
    pub default: Option<OptionValue>,
    /// Allowed values when `r#type == OptionType::Enum`.
    pub r#enum: &'static [&'static str],
}

impl OptionSpec {
    pub const fn new(
        name: &'static str,
        ty: OptionType,
        usage: &'static str,
    ) -> Self {
        Self {
            name,
            r#type: ty,
            usage,
            default: None,
            r#enum: &[],
        }
    }

    pub fn with_default(mut self, default: OptionValue) -> Self {
        self.default = Some(default);
        self
    }

    pub const fn with_enum(mut self, values: &'static [&'static str]) -> Self {
        self.r#enum = values;
        self
    }
}

/// Errors returned by [`parse_options`].
#[derive(Debug, Error, PartialEq)]
pub enum ParseError {
    #[error("empty option key in {0:?}")]
    EmptyKey(String),
    #[error("unknown option '{name}' (valid: {valid})")]
    Unknown { name: String, valid: String },
    #[error("option '{name}' requires a value (e.g. {name}=...)")]
    MissingValue { name: String },
    #[error("option '{name}': '{value}' is not an int")]
    NotInt { name: String, value: String },
    #[error("option '{name}': '{value}' is not a bool")]
    NotBool { name: String, value: String },
    #[error("option '{name}': '{value}' not in {{{valid}}}")]
    NotEnum {
        name: String,
        value: String,
        valid: String,
    },
}

/// Validate raw `"key=value"` (or `"key"` for bool flags) pairs against
/// `specs` and return the coerced map. Mirrors py
/// `parse_options`, ts `parseOptions`, go `ParseOptions`, php
/// `Options::parse`.
///
/// Coercion rules:
/// - [`OptionType::String`] — raw value passed through.
/// - [`OptionType::Int`] — parsed via `str::parse::<i64>()`.
/// - [`OptionType::Bool`] — `true|1|yes|t|y` → true; `false|0|no|f|n` → false (case-insensitive).
/// - [`OptionType::Enum`] — value must be in `spec.r#enum`.
/// - A pair without `=` is treated as `bool=true`; valid only when `r#type == Bool`.
/// - Defaults from `specs` fill in keys not present in `pairs`.
pub fn parse_options<I, S>(pairs: I, specs: &[OptionSpec]) -> Result<Options, ParseError>
where
    I: IntoIterator<Item = S>,
    S: AsRef<str>,
{
    let mut out: Options = HashMap::new();
    let names_csv = || {
        specs
            .iter()
            .map(|s| s.name)
            .collect::<Vec<_>>()
            .join(", ")
    };

    for raw_ref in pairs {
        let raw = raw_ref.as_ref();
        let (key, value, has_eq) = if let Some(eq_idx) = raw.find('=') {
            (&raw[..eq_idx], &raw[eq_idx + 1..], true)
        } else {
            (raw, "", false)
        };
        let key = key.trim();
        if key.is_empty() {
            return Err(ParseError::EmptyKey(raw.to_string()));
        }
        let spec = specs
            .iter()
            .find(|s| s.name == key)
            .ok_or_else(|| ParseError::Unknown {
                name: key.to_string(),
                valid: names_csv(),
            })?;

        if !has_eq {
            if spec.r#type != OptionType::Bool {
                return Err(ParseError::MissingValue {
                    name: key.to_string(),
                });
            }
            out.insert(key.to_string(), OptionValue::Bool(true));
            continue;
        }

        out.insert(key.to_string(), coerce(spec, value)?);
    }

    for spec in specs {
        if out.contains_key(spec.name) {
            continue;
        }
        if let Some(v) = &spec.default {
            out.insert(spec.name.to_string(), v.clone());
        }
    }

    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn spec(name: &'static str, ty: OptionType) -> OptionSpec {
        OptionSpec::new(name, ty, "")
    }

    #[test]
    fn parses_string_value() {
        let specs = [spec("delim", OptionType::String)];
        let out = parse_options(["delim=;"].iter(), &specs).unwrap();
        assert_eq!(out.get("delim"), Some(&OptionValue::String(";".into())));
    }

    #[test]
    fn parses_int_value() {
        let specs = [spec("indent", OptionType::Int)];
        let out = parse_options(["indent=4"].iter(), &specs).unwrap();
        assert_eq!(out.get("indent"), Some(&OptionValue::Int(4)));
    }

    #[test]
    fn parses_int_rejects_non_numeric() {
        let specs = [spec("indent", OptionType::Int)];
        let err = parse_options(["indent=abc"].iter(), &specs).unwrap_err();
        assert!(matches!(err, ParseError::NotInt { .. }));
    }

    #[test]
    fn bool_key_only_means_true() {
        let specs = [spec("flag", OptionType::Bool)];
        let out = parse_options(["flag"].iter(), &specs).unwrap();
        assert_eq!(out.get("flag"), Some(&OptionValue::Bool(true)));
    }

    #[test]
    fn key_only_rejected_for_non_bool() {
        let specs = [spec("delim", OptionType::String)];
        let err = parse_options(["delim"].iter(), &specs).unwrap_err();
        assert!(matches!(err, ParseError::MissingValue { .. }));
    }

    #[test]
    fn bool_common_forms() {
        let specs = [spec("flag", OptionType::Bool)];
        for tv in ["flag=true", "flag=1", "flag=yes", "flag=y", "flag=t"] {
            let out = parse_options([tv].iter(), &specs).unwrap();
            assert_eq!(out.get("flag"), Some(&OptionValue::Bool(true)));
        }
        for fv in ["flag=false", "flag=0", "flag=no", "flag=n", "flag=f"] {
            let out = parse_options([fv].iter(), &specs).unwrap();
            assert_eq!(out.get("flag"), Some(&OptionValue::Bool(false)));
        }
    }

    #[test]
    fn enum_validates_against_allowed() {
        let specs =
            [OptionSpec::new("case", OptionType::Enum, "").with_enum(&["upper", "lower"])];
        let out = parse_options(["case=upper"].iter(), &specs).unwrap();
        assert_eq!(out.get("case"), Some(&OptionValue::String("upper".into())));

        let err = parse_options(["case=mixed"].iter(), &specs).unwrap_err();
        assert!(matches!(err, ParseError::NotEnum { .. }));
    }

    #[test]
    fn unknown_key_lists_valid() {
        let specs = [
            spec("bar", OptionType::String),
            spec("baz", OptionType::String),
        ];
        let err = parse_options(["foo=1"].iter(), &specs).unwrap_err();
        match err {
            ParseError::Unknown { name, valid } => {
                assert_eq!(name, "foo");
                assert_eq!(valid, "bar, baz");
            }
            other => panic!("expected Unknown, got {:?}", other),
        }
    }

    #[test]
    fn defaults_fill_missing_keys() {
        let specs = [
            OptionSpec::new("a", OptionType::String, "").with_default(OptionValue::String("A".into())),
            OptionSpec::new("b", OptionType::Int, "").with_default(OptionValue::Int(7)),
        ];
        let out = parse_options(["a=override"].iter(), &specs).unwrap();
        assert_eq!(out.get("a"), Some(&OptionValue::String("override".into())));
        assert_eq!(out.get("b"), Some(&OptionValue::Int(7)));
    }

    #[test]
    fn empty_key_rejected() {
        let specs = [spec("x", OptionType::String)];
        let err = parse_options(["=value"].iter(), &specs).unwrap_err();
        assert!(matches!(err, ParseError::EmptyKey(_)));
    }
}

fn coerce(spec: &OptionSpec, value: &str) -> Result<OptionValue, ParseError> {
    match spec.r#type {
        OptionType::String => Ok(OptionValue::String(value.to_string())),
        OptionType::Int => value.parse::<i64>().map(OptionValue::Int).map_err(|_| {
            ParseError::NotInt {
                name: spec.name.to_string(),
                value: value.to_string(),
            }
        }),
        OptionType::Bool => match value.trim().to_ascii_lowercase().as_str() {
            "true" | "1" | "yes" | "t" | "y" => Ok(OptionValue::Bool(true)),
            "false" | "0" | "no" | "f" | "n" => Ok(OptionValue::Bool(false)),
            _ => Err(ParseError::NotBool {
                name: spec.name.to_string(),
                value: value.to_string(),
            }),
        },
        OptionType::Enum => {
            if spec.r#enum.iter().any(|e| *e == value) {
                Ok(OptionValue::String(value.to_string()))
            } else {
                Err(ParseError::NotEnum {
                    name: spec.name.to_string(),
                    value: value.to_string(),
                    valid: spec.r#enum.join(", "),
                })
            }
        }
    }
}
