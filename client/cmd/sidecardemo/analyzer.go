package sidecardemo

import "time"

// The demo's AI step: the model Hoop hosts (DEP-161) rates each read, and the
// one the guidance below calls high risk is held until the person at the
// terminal approves it. A small model at temperature 0 still needs the risk
// named for it: with the built-in guidance alone it rates a full export of
// the card numbers low. The guidance names the data, not the step, so a
// statement typed by hand is rated by the same rules.

// AnalyzerTimeout bounds one classification in the demo. Every read goes to
// the model, so with the model unreachable each one waits this long before
// the analyzer lets it through (fail open). Shorter than the analyzer's
// default, and shorter than the tour's step timeout.
const AnalyzerTimeout = 8 * time.Second

// AnalyzerPrompt is the lane guidance the demo config carries.
func AnalyzerPrompt(kind Kind) string {
	if kind == KindPostgres {
		return "You classify SQL sent to a shop database. Payment card data is the most sensitive data in it.\n" +
			"HIGH: a statement that reads payment card columns (creditcard, creditcardtype, creditcardexpiration), " +
			"or that reads customers without a LIMIT.\n" +
			"LOW: a read of a few rows (with a small LIMIT) that reads no payment card column.\n"
	}
	return "You classify HTTP requests sent to a users API. Bulk exports of user data are the most sensitive requests.\n" +
		"HIGH: a request whose path exports or downloads users in bulk (a path with export or download).\n" +
		"LOW: listing users, reading one user, or creating one.\n"
}

// AnalyzerTrigger is which operations the demo sends to the model: the reads,
// where its AI step is. Everything else is the guardrail's and the mask's.
func AnalyzerTrigger(kind Kind) []string {
	if kind == KindPostgres {
		return []string{"select"}
	}
	return []string{"get"}
}
