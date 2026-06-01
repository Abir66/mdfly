package slug

// reservedWords is the blocklist from CONTEXT.md § Reserved Words.
var reservedWords = map[string]struct{}{
	// Routes (live in v1)
	"api":     {},
	"cdn":     {},
	"healthz": {},
	"cli":     {},
	"auth":    {},
	"abuse":   {},
	"_":       {},
	"llm":     {}, // LLM twin prefix
	// Routes (planned v2)
	"dashboard": {},
	"settings":  {},
	"login":     {},
	"logout":    {},
	"signup":    {},
	"account":   {},
	"admin":     {},
	"docs":      {},
	"app":       {},
	// Static-asset + landing route namespaces (ADR-0023 / ADR-0024). _static is
	// outside the slug grammar so it needs no entry — only the Worker route.
	"landing": {},
	"static":  {},
	// Brand
	"mdfly": {},
	// DNS-confusion subdomains
	"www":  {},
	"mail": {},
	"ftp":  {},
	"smtp": {},
	// Marketing / legal pages
	"about":   {},
	"pricing": {},
	"terms":   {},
	"privacy": {},
	"contact": {},
}
