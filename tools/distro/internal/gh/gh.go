// Package gh wraps the GitHub CLI (gh) for resolving release tags and downloading assets.
package gh

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"proto-distro/tools/distro/internal/fileutil"
)

// CheckAuth verifies that the GitHub CLI is authenticated.
func CheckAuth() error {
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub CLI is not authenticated. Run 'gh auth login' to authenticate, then retry")
	}
	return nil
}

// ResolveLatestByPrefix finds the latest release tag in a repo matching a prefix.
// It searches through the most recent 50 releases.
func ResolveLatestByPrefix(repo, prefix string) (string, error) {
	cmd := exec.Command("gh", "release", "list", "--repo", repo, "--limit", "50", "--exclude-drafts", "--exclude-pre-releases")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("listing releases for %s: %w", repo, err)
	}

	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) >= 3 {
			tag := fields[2]
			if strings.HasPrefix(tag, prefix) {
				return tag, nil
			}
		}
	}

	return "", fmt.Errorf("no release matching prefix '%s' found in %s", prefix, repo)
}

// ResolveLatestTag gets the latest release tag for a repo.
func ResolveLatestTag(repo string) (string, error) {
	cmd := exec.Command("gh", "release", "view", "--repo", repo, "--json", "tagName", "--jq", ".tagName")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting latest release for %s: %w", repo, err)
	}
	tag := strings.TrimSpace(string(output))
	if tag == "" {
		return "", fmt.Errorf("empty tag returned for %s", repo)
	}
	return tag, nil
}

type releaseTagInfo struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type downloadDecision struct {
	Download bool
	Path     string
}

type DownloadResult struct {
	Matched    int      `json:"matched"`
	Downloaded []string `json:"downloaded"`
	Skipped    []string `json:"skipped"`
}

// DownloadAssets downloads release assets matching a pattern to a directory.
// Returns downloaded and skipped file lists, and how many release assets matched.
func DownloadAssets(repo, tag, pattern, destDir string) (DownloadResult, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return DownloadResult{}, err
	}

	assets, err := listReleaseAssets(repo, tag)
	if err != nil {
		return DownloadResult{}, err
	}

	result := DownloadResult{}
	for _, asset := range assets {
		matched, err := filepath.Match(pattern, asset.Name)
		if err != nil {
			return DownloadResult{}, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		if !matched {
			continue
		}
		result.Matched++

		localPath := filepath.Join(destDir, asset.Name)
		decision, err := shouldDownloadAsset(localPath, asset)
		if err != nil {
			return DownloadResult{}, err
		}
		if !decision.Download {
			result.Skipped = append(result.Skipped, decision.Path)
			continue
		}

		cmd := exec.Command("gh", "release", "download", tag,
			"--repo", repo,
			"--pattern", asset.Name,
			"--dir", destDir,
			"--clobber")
		// Keep CLI output on stderr so stdout can be used for machine-readable output.
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return DownloadResult{}, err
		}
		result.Downloaded = append(result.Downloaded, decision.Path)
	}

	sort.Strings(result.Downloaded)
	sort.Strings(result.Skipped)
	return result, nil
}

func listReleaseAssets(repo, tag string) ([]releaseAsset, error) {
	cmd := exec.Command("gh", "api", fmt.Sprintf("repos/%s/releases/tags/%s", repo, tag), "--jq", "{assets: .assets}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("fetching release assets for %s/%s: %w", repo, tag, err)
	}

	var info releaseTagInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("parsing release assets for %s/%s: %w", repo, tag, err)
	}
	return info.Assets, nil
}

func shouldDownloadAsset(localPath string, asset releaseAsset) (downloadDecision, error) {
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		absPath = localPath
	}

	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return downloadDecision{Download: true, Path: absPath}, nil
		}
		return downloadDecision{}, fmt.Errorf("checking %s: %w", localPath, err)
	}
	if info.IsDir() {
		return downloadDecision{Download: true, Path: absPath}, nil
	}

	// Size mismatch means the file cannot be the same artifact.
	if info.Size() != asset.Size {
		return downloadDecision{Download: true, Path: absPath}, nil
	}

	algo, expectedHash := parseDigest(asset.Digest)
	if algo == "sha256" && expectedHash != "" {
		actualHash, err := fileutil.SHA256File(localPath)
		if err != nil {
			return downloadDecision{}, fmt.Errorf("hashing %s: %w", localPath, err)
		}
		if !strings.EqualFold(actualHash, expectedHash) {
			return downloadDecision{Download: true, Path: absPath}, nil
		}
	}

	return downloadDecision{Download: false, Path: absPath}, nil
}

func parseDigest(digest string) (algo string, value string) {
	parts := strings.SplitN(strings.TrimSpace(digest), ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.ToLower(parts[0]), strings.TrimSpace(parts[1])
}

// ReleaseTagInfo holds the tag name from a GitHub release.
type ReleaseTagInfo struct {
	TagName string `json:"tagName"`
}

// ResolveTag resolves a tag string: "latest" auto-resolves using the optional
// prefix, otherwise verifies the tag exists via the GitHub API.
func ResolveTag(tag, repo, prefix string) (string, error) {
	if tag == "latest" {
		if prefix != "" {
			return ResolveLatestByPrefix(repo, prefix)
		}
		return ResolveLatestTag(repo)
	}
	return GetReleaseTag(repo, tag)
}

// GetReleaseTag gets the tag name for a specific release.
func GetReleaseTag(repo, tag string) (string, error) {
	cmd := exec.Command("gh", "release", "view", tag, "--repo", repo, "--json", "tagName")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getting release %s from %s: %w", tag, repo, err)
	}
	var info ReleaseTagInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return "", fmt.Errorf("parsing release info: %w", err)
	}
	return info.TagName, nil
}
