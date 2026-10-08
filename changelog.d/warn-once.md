level: patch

Under `tdd = warn` the stop message names each unseen red once per session instead of at every stop. Before, a red that no hook reached, for example in a lane nobody works in any more, was repeated at the end of every turn. A red in a checkout that is gone from the disk is no longer counted at all, and that applies under `enforce` too. The red itself is still reported by the next hook in its checkout.
