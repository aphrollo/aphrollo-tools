package tdd

import "testing"

// widgetHead is a production function with an inline #[cfg(test)] module, the
// shape issue #714 is about. Each test below edits one side of it and asks the
// splitter which side moved.
const widgetHead = `pub fn widget<'a>(name: &'a str) -> &'a str {
    if name.is_empty() { "}" } else { name }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn widget_echoes_its_name() {
        assert_eq!(widget("a"), "a");
    }
}
`

func mustSplitRust(t *testing.T, src string) rustSplit {
	t.Helper()
	s, ok := splitRustTests(src, false)
	if !ok {
		t.Fatalf("splitRustTests refused a balanced file:\n%s", src)
	}
	return s
}

func TestSplitRustTests_TestBodyEditLeavesProductionIdentical(t *testing.T) {
	head := mustSplitRust(t, widgetHead)
	edited := mustSplitRust(t, `pub fn widget<'a>(name: &'a str) -> &'a str {
    if name.is_empty() { "}" } else { name }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn widget_echoes_its_name() {
        assert_eq!(widget("a"), "a");
    }

    #[test]
    fn widget_keeps_an_empty_name_as_a_brace() {
        assert_eq!(widget(""), "}");
    }
}
`)
	if edited.prod != head.prod {
		t.Fatalf("adding a test inside #[cfg(test)] changed the production projection:\nhead: %q\nedit: %q", head.prod, edited.prod)
	}
	if edited.region == head.region {
		t.Fatal("adding a test did not change the test region")
	}
	if _, ok := edited.tests["widget_keeps_an_empty_name_as_a_brace"]; !ok {
		t.Fatalf("the added test is missing from the test map: %v", edited.tests)
	}
	if edited.tests["widget_echoes_its_name"] != head.tests["widget_echoes_its_name"] {
		t.Fatal("an untouched test's body hash moved")
	}
}

func TestSplitRustTests_ProductionEditChangesProduction(t *testing.T) {
	head := mustSplitRust(t, widgetHead)
	edited := mustSplitRust(t, `pub fn widget<'a>(name: &'a str) -> &'a str {
    if name.is_empty() { "{" } else { name }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn widget_echoes_its_name() {
        assert_eq!(widget("a"), "a");
    }
}
`)
	if edited.prod == head.prod {
		t.Fatal("changing a string literal in production code left the production projection unchanged")
	}
	if edited.region != head.region {
		t.Fatal("a production-only edit moved the test region")
	}
}

func TestSplitRustTests_ReformatIsNotAChange(t *testing.T) {
	head := mustSplitRust(t, widgetHead)
	reformatted := mustSplitRust(t, `pub fn widget<'a>(name: &'a str) -> &'a str {
    if name.is_empty() {
        "}"
    } else {
        name
    }
}
// a comment is not behaviour
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn widget_echoes_its_name() {
        assert_eq!( widget("a"), "a" );
    }
}
`)
	if reformatted.prod != head.prod || reformatted.region != head.region {
		t.Fatal("a whitespace/comment-only rewrite read as a change")
	}
	if reformatted.tests["widget_echoes_its_name"] != head.tests["widget_echoes_its_name"] {
		t.Fatal("reformatting a test moved its body hash")
	}
}

func TestSplitRustTests_TestBodyChangeMovesOnlyThatTestsHash(t *testing.T) {
	head := mustSplitRust(t, widgetHead)
	edited := mustSplitRust(t, `pub fn widget<'a>(name: &'a str) -> &'a str {
    if name.is_empty() { "}" } else { name }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn widget_echoes_its_name() {
        assert_eq!(widget("b"), "b");
    }
}
`)
	if edited.tests["widget_echoes_its_name"] == head.tests["widget_echoes_its_name"] {
		t.Fatal("rewriting an assertion left the test's body hash unchanged")
	}
}

// A #[should_panic] written before #[test] is part of what the test asserts.
func TestSplitRustTests_LeadingAttributeBelongsToTheTest(t *testing.T) {
	with := mustSplitRust(t, "fn f() {}\n#[cfg(test)]\nmod tests {\n    #[should_panic]\n    #[test]\n    fn boom() { panic!() }\n}\n")
	without := mustSplitRust(t, "fn f() {}\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn boom() { panic!() }\n}\n")
	if with.tests["boom"] == without.tests["boom"] {
		t.Fatal("dropping #[should_panic] left the test's body hash unchanged")
	}
}

func TestSplitRustTests_StringThatLooksLikeCfgTestIsProduction(t *testing.T) {
	head := mustSplitRust(t, "pub fn s() -> &'static str { r#\"#[cfg(test)] mod x { }\"# }\n")
	edited := mustSplitRust(t, "pub fn s() -> &'static str { r#\"#[cfg(test)] mod x { y }\"# }\n")
	if edited.prod == head.prod {
		t.Fatal("a raw string mentioning #[cfg(test)] was read as a test module")
	}
}

func TestSplitRustTests_UnbalancedFileIsRefused(t *testing.T) {
	if _, ok := splitRustTests("fn f() {\n#[cfg(test)]\nmod tests {\n", false); ok {
		t.Fatal("an unbalanced file must be refused, not split")
	}
	if _, ok := splitRustTests("fn f() { \"unterminated }\n", false); ok {
		t.Fatal("an unterminated string must be refused, not split")
	}
}

// A file mounted by `#[cfg(test)] #[path = "..."] mod tests;` is test code
// from its first byte to its last.
func TestSplitRustTests_WholeFileTestHasNoProduction(t *testing.T) {
	s, ok := splitRustTests("use super::*;\n\n#[test]\nfn widget_echoes_its_name() {\n    assert_eq!(widget(\"a\"), \"a\");\n}\n", true)
	if !ok {
		t.Fatal("whole-file test refused")
	}
	if s.prod != "" {
		t.Fatalf("a whole-file test carried production text: %q", s.prod)
	}
	if _, ok := s.tests["widget_echoes_its_name"]; !ok {
		t.Fatalf("the whole-file test's #[test] fn is missing: %v", s.tests)
	}
}

func TestSplitRustTests_DuplicateNameIsAmbiguous(t *testing.T) {
	s := mustSplitRust(t, "#[cfg(test)]\nmod a {\n    #[test]\n    fn same() {}\n}\n#[cfg(test)]\nmod b {\n    #[test]\n    fn same() { assert!(true) }\n}\n")
	if s.tests["same"] != ambiguousTest {
		t.Fatalf("two tests named `same` must be ambiguous, got %q", s.tests["same"])
	}
}

// support is the test code a test leans on without being a test: a helper
// change moves it, adding another test does not.
func TestSplitRustTests_SupportTracksHelpersNotTests(t *testing.T) {
	base := mustSplitRust(t, "fn f() {}\n#[cfg(test)]\nmod tests {\n    fn want() -> i32 { 2 }\n    #[test]\n    fn a() { assert_eq!(want(), 2) }\n}\n")
	added := mustSplitRust(t, "fn f() {}\n#[cfg(test)]\nmod tests {\n    fn want() -> i32 { 2 }\n    #[test]\n    fn a() { assert_eq!(want(), 2) }\n    #[test]\n    fn b() {}\n}\n")
	helper := mustSplitRust(t, "fn f() {}\n#[cfg(test)]\nmod tests {\n    fn want() -> i32 { 3 }\n    #[test]\n    fn a() { assert_eq!(want(), 2) }\n}\n")
	if base.support == "" || added.support != base.support {
		t.Fatalf("adding a test moved the support hash: %q -> %q", base.support, added.support)
	}
	if helper.support == base.support {
		t.Fatal("changing a helper left the support hash unchanged")
	}
	if helper.tests["a"] != base.tests["a"] {
		t.Fatal("a helper change moved the test's own hash")
	}
}
