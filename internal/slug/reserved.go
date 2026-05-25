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
