package cli

import "github.com/aphrollo/aphrollo-tools/internal/undercover"

// undercoverTextRefusal is the check `issue` and `feedback` run on the title
// and body they are about to hand to gh: the refusal quoting the first line
// that carries a tell, "" when there is none or root never set
// `undercover = true`. Each text is a pair: what it is, then the text.
func undercoverTextRefusal(root string, texts ...[2]string) string {
	tells, on := undercover.Load(root)
	if !on {
		return ""
	}
	for _, t := range texts {
		if h, hit := tells.Text(t[1]); hit {
			return undercover.TextRefusal(t[0], h)
		}
	}
	return ""
}
