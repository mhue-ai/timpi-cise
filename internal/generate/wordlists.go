package generate

// Built-in vocabulary used by the algorithmic generators. Kept deliberately
// broad and non-sensitive so generated queries resemble ordinary human search
// interest across many topics.

var topics = []string{
	"privacy", "search engine", "open source software", "renewable energy",
	"machine learning", "home gardening", "electric vehicles", "space exploration",
	"quantum computing", "coffee brewing", "mountain biking", "digital art",
	"personal finance", "climate change", "ancient history", "marine biology",
	"web development", "cybersecurity", "sustainable farming", "photography",
	"classical music", "board games", "3d printing", "urban planning",
	"nutrition", "astronomy", "linguistics", "cryptography", "robotics",
	"typography", "cooking techniques", "wildlife conservation", "geology",
	"data privacy", "decentralized networks", "solar panels", "meditation",
	"language learning", "chess strategy", "microbiomes", "electric guitars",
}

var adjectives = []string{
	"best", "affordable", "beginner", "advanced", "modern", "reliable",
	"lightweight", "secure", "efficient", "portable", "durable", "compact",
	"open", "private", "fast", "simple", "powerful", "eco friendly",
	"low cost", "high quality",
}

var nouns = []string{
	"guide", "tutorial", "tips", "comparison", "review", "alternatives",
	"tools", "examples", "basics", "checklist", "roadmap", "resources",
	"techniques", "benefits", "risks", "history", "future", "trends",
	"software", "hardware",
}

var connectors = []string{
	"for", "without", "vs", "with", "near", "in 2026", "at home", "for beginners",
	"on a budget", "explained",
}

// Question scaffolds; %s is replaced with a topic.
var questionTemplates = []string{
	"how does %s actually work",
	"what is the best way to learn %s",
	"why is %s important for everyday users",
	"what are the main risks of %s",
	"how do I get started with %s",
	"what are the alternatives to %s",
	"is %s worth it in 2026",
	"how has %s changed over the last decade",
	"what should beginners know about %s",
	"how do experts approach %s",
	"what are common mistakes in %s",
	"how do I compare options for %s",
	"what is the difference between %s and its alternatives",
	"how much does %s typically cost",
	"what tools do professionals use for %s",
}

// Phrase scaffolds; first %s adjective, second %s topic, third %s connector.
var phraseTemplates = []string{
	"%s %s %s",            // adjective topic connector
	"%s %s",               // adjective topic
	"%s %s guide",         // adjective topic guide
	"how to choose %s %s", // adjective topic (connector unused here)
}
