package workspace

import "fmt"

// cargoLaneTemplate is the pair cargoLaneLevel hands out a copy of, one per
// distinct (beforePR, level): a one-crate cargo workspace on main, pushed to
// origin, and a lane branch that changes the crate's one source line, pushed.
func cargoLaneTemplate(beforePR bool, level string) originPair {
	return derivedPair(fmt.Sprintf("cargo-lane/%v/%s", beforePR, level), remoteTemplate, func(work string) {
		manifest := "[workspace]\nmembers = [\"crates/a\"]\n"
		if beforePR {
			manifest += "\n[workspace.metadata.aphrollo]\nmutants-before-pr = true\n" + level
		}
		writeTemplateFile(work, "Cargo.toml", manifest)
		writeTemplateFile(work, "crates/a/Cargo.toml", "[package]\nname = \"a\"\nversion = \"0.1.0\"\n")
		writeTemplateFile(work, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b }\n")
		originTemplateGit(work, "add", ".")
		originTemplateGit(work, "commit", "-qm", "trunk")
		originTemplateGit(work, "push", "-q", "origin", "main")
		originTemplateGit(work, "checkout", "-q", "-b", "lane")
		writeTemplateFile(work, "crates/a/src/lib.rs", "pub fn add(a: i32, b: i32) -> i32 { a + b + 0 }\n")
		originTemplateGit(work, "add", ".")
		originTemplateGit(work, "commit", "-qm", "lane")
		originTemplateGit(work, "push", "-q", "-u", "origin", "lane")
	})
}
