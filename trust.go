package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

var canWrite = func(string) bool { return false }

func writeChecker(repo string) func(string) bool {
	cache := map[string]bool{}
	return func(login string) bool {
		if login == "" {
			return false
		}
		if ok, seen := cache[login]; seen {
			return ok
		}
		perm, err := ghOwner(repo, "api", "repos/{owner}/{repo}/collaborators/"+login+"/permission", "--jq", ".permission")
		perm = strings.TrimSpace(perm)
		ok := err == nil && (perm == "admin" || perm == "maintain" || perm == "write")
		cache[login] = ok
		return ok
	}
}

func trusted(c prComment) bool {
	switch c.Association {
	case "OWNER":
		return true
	case "MEMBER", "COLLABORATOR":
		return canWrite(c.Author.Login)
	}
	return false
}

func issueDigest(is issue) string {
	sum := sha256.Sum256([]byte(is.Title + "\x00" + is.Body))
	return hex.EncodeToString(sum[:])
}

func editedSinceGroom(is issue, st cardState) bool {
	return st.Groomed != "" && st.Groomed != issueDigest(is)
}

func needsApproval(is issue, st cardState) bool {
	return !canWrite(is.Author.Login) && st.Approved != issueDigest(is)
}
