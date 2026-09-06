package runtime

import "os"

// Environment variables let deployments point one process at several ML
// capabilities without putting host-specific command lines in config.yaml.
// A helper command remains optional and Knowledge-owned.
func osHelperCommand(capability string) string {
	switch capability {
	case CapabilityEmbedding:
		return os.Getenv("SHUTU_KNOWLEDGE_EMBEDDING_HELPER")
	case CapabilityRerank:
		return os.Getenv("SHUTU_KNOWLEDGE_RERANK_HELPER")
	case CapabilityOCR:
		return os.Getenv("SHUTU_KNOWLEDGE_OCR_HELPER")
	default:
		return ""
	}
}
