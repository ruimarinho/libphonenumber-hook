package function

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v32/github"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	ghooks "gopkg.in/go-playground/webhooks.v5/github"
)

// Handle a function invocation
func Handle(w http.ResponseWriter, r *http.Request) {
	hook, _ := ghooks.New()
	payload, err := hook.Parse(r, ghooks.PushEvent)
	if err != nil {
		log.Panic(err)
	}

	HandleEvent(payload)

	w.Write([]byte("OK"))
	w.WriteHeader(http.StatusOK)
}

// HandleEvent handles multiple GitHub events.
func HandleEvent(payload interface{}) {
	log.WithField("payload", payload).Info("Handling incoming libphonenumber-webhook")

	push := payload.(ghooks.PushPayload)

	if !strings.Contains(push.Ref, "refs/tags/") {
		log.Warn("Push reference is not a tag, skipping")
		return
	}

	version := strings.Replace(push.Ref, "refs/tags/v", "", -1)

	log.Info("Received push payload for version v", version)

	directory, repo, err := Clone()
	if err != nil {
		log.Panic(err)
	}

	file, err := Download(version)
	if err != nil {
		log.Panic(err)
	}

	err = Extract(file, directory)
	if err != nil {
		log.Panic(err)
	}

	err = Commit(version, repo, &CommitOptions{Push: true})
	if err != nil {
		log.Panic(err)
	}

	OpenPullRequest(version)
}

// CommitOptions holds information about commit options.
type CommitOptions struct {
	Push bool
}

func Clone() (string, *git.Repository, error) {
	customClient := &http.Client{
		// 15 second timeout
		Timeout: 15 * time.Second,
	}

	// Override http(s) default protocol to use our custom client
	client.InstallProtocol("https", githttp.NewClient(customClient))

	directory, err := ioutil.TempDir("", "libphonenumber")
	if err != nil {
		return directory, nil, err
	}

	log.Infof("Cloning ruimarinho/google-libphonenumber to %s", directory)

	repo, err := git.PlainClone(directory, false, &git.CloneOptions{
		URL:      "https://github.com/ruimarinho/google-libphonenumber.git",
		Progress: os.Stdout,
	})

	if err != nil {
		return directory, nil, err
	}

	log.Infof("Cloned ruimarinho/google-libphonenumber to %s", directory)

	return directory, repo, nil
}

// Commit creates a branch and commits on that branch the modified index tree.
func Commit(version string, repo *git.Repository, options *CommitOptions) error {
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}

	tag := strings.Replace(version, ".", "-", -1)
	err = worktree.Checkout(&git.CheckoutOptions{
		Create: true,
		Branch: plumbing.ReferenceName(fmt.Sprintf("refs/heads/support/update-libphonenumber-%s", tag)),
		Force:  true,
	})

	if err != nil {
		return err
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

	log.Infof("Committed %s", commit.String())

	if !options.Push {
		log.Warn("Skipping commit push")
		return nil
	}

	err = push(version, repo)
	if err != nil {
		return err
	}

	return nil
}

// Push commit to remote origin.
func push(version string, repo *git.Repository) error {
	remote, err := repo.Remote("origin")
	if err != nil {
		return err
	}

	log.Infof("Pushing to remote origin %s", remote.Config().URLs[0])

	tag := strings.Replace(version, ".", "-", -1)
	pushOptions := git.PushOptions{
		RefSpecs: []config.RefSpec{config.RefSpec(fmt.Sprintf("refs/heads/support/update-libphonenumber-%s:refs/heads/support/update-libphonenumber-%s", tag, tag))},
		Auth: &githttp.BasicAuth{
			Username: "ruimarinho",
			Password: os.Getenv("GITHUB_TOKEN"),
		},
		Progress: os.Stdout,
	}

	err = remote.Push(&pushOptions)
	if err != nil {
		return err
	}

	log.Infof("Pushed to %s successfully", fmt.Sprintf("support/update-libphonenumber-%s", tag))

	return nil
}

func OpenPullRequest(version string) error {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	pull, _, err := client.PullRequests.Create(ctx, "ruimarinho", "google-libphonenumber", &github.NewPullRequest{
		Title: github.String(fmt.Sprintf("Update libphonenumber@%s", version)),
		Head:  github.String(fmt.Sprintf("support/update-libphonenumber-%s", strings.Replace(version, ".", "-", -1))),
		Base:  github.String("master"),
		Body:  github.String(fmt.Sprintf("Update libphonenumber@%s.", version)),
	})

	if err != nil {
		return err
	}

	log.Info(fmt.Sprintf("Pull request #%d opened (%v)", *pull.Number, *pull.HTMLURL))

	return nil
}

func Extract(file io.ReadCloser, directory string) error {
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		log.Panic(err)
	}

	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, errRead := tarReader.Next()
		if errRead == io.EOF {
			break
		}

		if errRead != nil {
			return errRead
		}

		name := header.Name

		if header.Typeflag != tar.TypeReg || !strings.Contains(name, "javascript/i18n/phonenumbers/") {
			continue
		}

		path := fmt.Sprintf("%s/src/", directory) + filepath.Base(name)
		log.WithField("file", path).Info("Extracting file")

		targetFile, errFile := os.OpenFile(path, os.O_CREATE|os.O_RDWR, header.FileInfo().Mode())
		if errFile != nil {
			return errFile
		}

		defer targetFile.Close()

		if _, copyErr := io.Copy(targetFile, tarReader); err != nil {
			return copyErr
		}
	}

	return nil
}

func Download(version string) (io.ReadCloser, error) {
	resp, err := http.Get(fmt.Sprintf("https://github.com/googlei18n/libphonenumber/archive/v%s.tar.gz", version))

	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}
