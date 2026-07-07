package imageref

import "strings"

// DockerHubHost is the normalized registry host for Docker Hub (go-containerregistry).
const DockerHubHost = "index.docker.io"

// RuleLookupKeys returns rules[].image keys to try when matching a running container.
//
// On Docker Hub, official images use library/<name> in OCI form while compose often
// shows <name> only; both alias each other. All other registries use exact repo paths.
func RuleLookupKeys(host, repo string) []string {
	keys := []string{repo}
	if host != DockerHubHost {
		return keys
	}
	if name, ok := strings.CutPrefix(repo, "library/"); ok && name != "" && !strings.Contains(name, "/") {
		keys = append(keys, name)
	} else if !strings.Contains(repo, "/") {
		keys = append(keys, "library/"+repo)
	}
	return keys
}

// RuleConfigKeys returns lookup keys claimed by a rules[].image value (duplicate detection).
func RuleConfigKeys(image string) []string {
	return RuleLookupKeys(DockerHubHost, image)
}
