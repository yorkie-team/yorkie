//! A grow-only counter. Written for the cargo rule set's tests: a product
//! type with its unit tests in a `mod tests` at the bottom, the layout most
//! Rust source files have.

use std::collections::BTreeMap;

/// Per-actor counts.
#[derive(Debug, Default, Clone, PartialEq)]
pub struct Counter {
    counts: BTreeMap<String, u64>,
}

impl Counter {
    /// Adds `by` to `actor`'s count.
    pub fn increase(&mut self, actor: &str, by: u64) {
        *self.counts.entry(actor.to_owned()).or_insert(0) += by;
    }

    /// The total over every actor.
    pub fn value(&self) -> u64 {
        self.counts.values().sum()
    }

    /// Merges `other` in, keeping the larger count per actor.
    pub fn merge<'a>(&'a mut self, other: &Counter) -> &'a mut Self {
        for (actor, count) in &other.counts {
            let mine = self.counts.entry(actor.clone()).or_insert(0);
            if *count > *mine {
                *mine = *count;
            }
        }
        debug_assert!(self.value() >= other.value());
        self
    }

    /* A block comment with a brace { and a nested /* comment } */ in it. */
    fn label(&self) -> &'static str {
        // Brackets that are not brackets: in a char, a string, a raw string,
        // a byte char and an escaped quote.
        let _ = ('{', "}", r#"{"}"#, b'[', '\'', "\"(");
        "counter"
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn increases_per_actor() {
        let mut c = Counter::default();
        c.increase("a", 2);
        c.increase("b", 3);
        assert_eq!(c.value(), 5);
    }

    #[test]
    fn merge_keeps_the_larger_count() {
        let mut a = Counter::default();
        a.increase("x", 4);
        let mut b = Counter::default();
        b.increase("x", 1);
        a.merge(&b);
        assert_eq!(a.value(), 4);
        assert!(a.label().starts_with("count"));
    }
}
