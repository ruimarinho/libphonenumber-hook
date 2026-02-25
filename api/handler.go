package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v68/github"
	log "github.com/sirupsen/logrus"
	webhook "gopkg.in/go-playground/webhooks.v5/github"
)

// Filenames lists all javascript files subject to modification
// on the upstream repository. Not ideal, but this is a workaround
// for Vercel's hard resource limits on its free tier (15 seconds of
// maximum runtime and 5MB of downloads).
var filenames = []string{
	"asyoutypeformatter.js",
	"asyoutypeformatter_test.js",
	"demo-compiled.js",
	"demo.js",
	"metadata.js",
	"metadatafortesting.js",
	"metadatalite.js",
	"phonemetadata.pb.js",
	"phonenumber.pb.js",
	"phonenumberutil.js",
	"phonenumberutil_test.js",
	"regioncodefortesting.js",
	"shortnumberinfo.js",
	"shortnumberinfo_test.js",
	"shortnumbermetadata.js",
}

const (
	remoteRepositoryUsername = "ruimarinho"
	remoteRepositoryName     = "google-libphonenumber"
	remoteBranchFormat       = "support/update-libphonenumber-%s"
)

// CommitOptions holds information about commit options.
type CommitOptions struct {
	Push bool
}

// Handler is called automatically by Vercel Serverless platform.
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not supported by libphonenumber-hook"))
		return
	}

	hook, err := webhook.New()
	if err != nil {
		log.WithError(err).Error("Failed to create webhook")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	payload, err := hook.Parse(r, webhook.PushEvent)
	if err != nil {
		log.WithError(err).Error("Failed to parse webhook payload")
		http.Error(w, "Failed to parse webhook", http.StatusBadRequest)
		return
	}

	if err := HandleEvent(payload); err != nil {
		log.WithError(err).Error("Failed to handle event")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Write([]byte("OK"))
}

func extractVersion(ref string) string {
	return strings.ReplaceAll(ref, "refs/tags/v", "")
}

// HandleEvent handles multiple GitHub events.
func HandleEvent(payload any) error {
	log.WithField("payload", payload).Info("Handling incoming webhook")

	push := payload.(webhook.PushPayload)
	if !strings.Contains(push.Ref, "refs/tags/") {
		log.Warn("Push reference is not a tag, skipping")
		return nil
	}

	version := extractVersion(push.Ref)

	log.Infof("Received push payload for version v%s", version)

	directory, repository, err := Clone(fmt.Sprintf("%s/%s", remoteRepositoryUsername, remoteRepositoryName))
	if err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	err = Commit(version, directory, repository, &CommitOptions{Push: true})
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	err = OpenPullRequest(version)
	if err != nil {
		return fmt.Errorf("open pull request: %w", err)
	}

	return nil
}

// Clone a repository into a temporary folder.
func Clone(repositoryName string) (string, *git.Repository, error) {
	directory, err := os.MkdirTemp("", strings.ReplaceAll(repositoryName, "/", "-"))
	if err != nil {
		return directory, nil, err
	}

	log.Infof("Cloning %s to %s", repositoryName, directory)

	gitRepository, err := git.PlainClone(directory, false, &git.CloneOptions{
		URL:           fmt.Sprintf("https://github.com/%s.git", repositoryName),
		ReferenceName: plumbing.ReferenceName("refs/heads/master"),
		Progress:      os.Stdout,
	})
	if err != nil {
		return directory, nil, err
	}

	log.Infof("Cloned %s into %s", repositoryName, directory)

	return directory, gitRepository, nil
}

// Commit creates a branch and commits the modified index tree on that branch.
func Commit(version string, directory string, repository *git.Repository, options *CommitOptions) error {
	worktree, err := repository.Worktree()
	if err != nil {
		return err
	}

	err = worktree.Checkout(&git.CheckoutOptions{
		Create: true,
		Branch: plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", fmt.Sprintf(remoteBranchFormat, strings.ReplaceAll(version, ".", "-")))),
		Force:  true,
	})
	if err != nil {
		return err
	}

	for _, filename := range filenames {
		_, err := Download(fmt.Sprintf("google/libphonenumber/v%s/javascript/i18n/phonenumbers/%s", version, filename), fmt.Sprintf("%s/src", directory))
		if err != nil {
			return fmt.Errorf("download %s: %w", filename, err)
		}
	}

	commit, err := worktree.Commit(fmt.Sprintf("Update libphonenumber@%s", version), &git.CommitOptions{
		All: true,
		Author: &object.Signature{
			Name:  "Rui Marinho",
			Email: "ruipmarinho@gmail.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return err
	}

	log.Infof("Git commit %s", commit.String())

	if !options.Push {
		log.Warn("Skipping commit push")
		return nil
	}

	remote, err := repository.Remote("origin")
	if err != nil {
		return err
	}

	log.Infof("Pushing to remote origin %s", remote.Config().URLs[0])

	tag := strings.ReplaceAll(version, ".", "-")
	pushOptions := git.PushOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", fmt.Sprintf(remoteBranchFormat, tag), fmt.Sprintf(remoteBranchFormat, tag)))},
		Force:    true,
		Auth: &githttp.BasicAuth{
			Username: remoteRepositoryUsername,
			Password: os.Getenv("GITHUB_TOKEN"),
		},
		Progress: os.Stdout,
	}

	err = remote.Push(&pushOptions)
	if err != nil {
		return err
	}

	log.Infof("Pushed to %s successfully", fmt.Sprintf(remoteBranchFormat, tag))

	return nil
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

// Download a file path into a target directory.
func Download(path string, directory string) (*os.File, error) {
	filename := filepath.Base(path)
	file, err := os.Create(fmt.Sprintf("%s/%s", directory, filename))
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s", path)

	if err != nil {
		return nil, err
	}

	defer file.Close()

	log.Infof("Downloading %s from %s into directory %s", filename, url, directory)

	response, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %d", url, response.StatusCode)
	}

	_, err = io.Copy(file, response.Body)
	if err != nil {
		return nil, fmt.Errorf("copy %s: %w", filename, err)
	}

	log.Infof("File %s downloaded successfully", path)

	return file, nil
}

// OpenPullRequest opens a pull request for a specific branch.
func OpenPullRequest(version string) error {
	ctx := context.Background()
	client := github.NewClient(nil).WithAuthToken(os.Getenv("GITHUB_TOKEN"))
	pull, _, err := client.PullRequests.Create(ctx, remoteRepositoryUsername, remoteRepositoryName, &github.NewPullRequest{
		Title: github.Ptr(fmt.Sprintf("Update libphonenumber@%s", version)),
		Head:  github.Ptr(fmt.Sprintf(remoteBranchFormat, strings.ReplaceAll(version, ".", "-"))),
		Base:  github.Ptr("master"),
		Body:  github.Ptr(fmt.Sprintf("Update libphonenumber@%s.", version)),
	})

	if err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Pull request #%d opened (%v)", *pull.Number, *pull.HTMLURL))

	return nil
}
