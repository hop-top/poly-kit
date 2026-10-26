//! The topics a [`StateMachine`] publishes on, and how to override them.
//! Go's statemachine_topics.go.

use crate::bus::{prefix_topics, Topic};

use super::quote::quote;
use super::statemachine::StateMachine;

/// The actions a prefix is expanded with, in Go's order, so the first
/// failing action reported is the same.
const ACTIONS: [&str; 2] = ["pre_transitioned", "post_transitioned"];

/// The kit baseline pre-transition topic.
pub const DEFAULT_PRE_TRANSITIONED: &str = "kit.runtime.state.pre_transitioned";
/// The kit baseline post-transition topic.
pub const DEFAULT_POST_TRANSITIONED: &str = "kit.runtime.state.post_transitioned";

/// The per-phase topics. Go's `StateMachineTopics`.
///
/// One event per phase: `pre_transitioned` (on the caller's thread,
/// veto-able) before the transition and `post_transitioned`
/// (fire-and-forget) after it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StateMachineTopics {
    /// The veto-able event before the transition.
    pub pre_transitioned: Topic,
    /// The fire-and-forget event after it.
    pub post_transitioned: Topic,
}

impl Default for StateMachineTopics {
    fn default() -> Self {
        default_state_machine_topics()
    }
}

/// The kit baseline, used when no override is given. Go's
/// `DefaultStateMachineTopics`. Both topics pass
/// [`validate_topic`](crate::bus::validate_topic).
pub fn default_state_machine_topics() -> StateMachineTopics {
    StateMachineTopics {
        pre_transitioned: Topic::from(DEFAULT_PRE_TRANSITIONED),
        post_transitioned: Topic::from(DEFAULT_POST_TRANSITIONED),
    }
}

impl StateMachine {
    /// Sets both topics from a 3-segment prefix,
    /// `source.category.object`: `<prefix>.pre_transitioned` and
    /// `<prefix>.post_transitioned`. Go's `WithSMTopicPrefix`.
    ///
    /// ```
    /// # use hop_top_kit::domain::{Rules, StateMachine};
    /// let sm = StateMachine::new(Rules::new(), None).with_topic_prefix("myapp.task.state");
    /// assert_eq!(sm.topics().pre_transitioned.as_str(), "myapp.task.state.pre_transitioned");
    /// ```
    ///
    /// # Panics
    ///
    /// If [`prefix_topics`] rejects `prefix`, with Go's text:
    /// `domain.WithSMTopicPrefix("<prefix>"): <reason>`. Machines are
    /// wired at boot, so a bad prefix is a programmer error; failing loud
    /// beats a silent default that subscribers would miss.
    pub fn with_topic_prefix(mut self, prefix: &str) -> Self {
        let (mut map, err) = prefix_topics(prefix, &ACTIONS);
        if let Some(err) = err {
            panic!("domain.WithSMTopicPrefix({}): {err}", quote(prefix));
        }
        // Without an error the map holds every action.
        let mut take = |action: &str| map.remove(action).expect("prefix_topics maps every action");
        self.topics = StateMachineTopics {
            pre_transitioned: take(ACTIONS[0]),
            post_transitioned: take(ACTIONS[1]),
        };
        self
    }

    /// Replaces the topics; an empty field keeps the default for that
    /// phase, so one phase can be overridden alone. Go's `WithSMTopics`.
    pub fn with_topics(mut self, topics: StateMachineTopics) -> Self {
        let defaults = default_state_machine_topics();
        let pick = |t: Topic, default: Topic| if t.as_str().is_empty() { default } else { t };
        self.topics = StateMachineTopics {
            pre_transitioned: pick(topics.pre_transitioned, defaults.pre_transitioned),
            post_transitioned: pick(topics.post_transitioned, defaults.post_transitioned),
        };
        self
    }

    /// The topics this machine publishes on. Go keeps them private.
    pub fn topics(&self) -> &StateMachineTopics {
        &self.topics
    }
}
