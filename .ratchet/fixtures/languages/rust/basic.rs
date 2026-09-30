// it's a comment
fn keep<'a>(x: &'a str) -> &'a str { "don't" }
/* block 'with' a quote */
#[test]
fn adds() { let c = 'x'; let n = '\n'; assert_eq!(c, 'x'); }
#[tokio::test]
async fn later() { let q = '\''; }
'outer: loop { break 'outer; }
