package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/sourcegraph/conc/pool"
	"github.com/yosssi/gohtml"
)

func main() {
	ctx := context.Background()

	gitDir := "/tmp/confluence_dump"
	base := "https://wiki.nci.nih.gov"
	next := "/rest/api/content?limit=25"

	for {
		if next == "" {
			break
		}

		url := fmt.Sprintf("%s%s", base, next)
		println("listing", url)
		listResponse, err := listContent(ctx, url)
		if err != nil {
			panic(err)
		}

		err = handleListResponse(ctx, gitDir, listResponse)
		if err != nil {
			panic(err)
		}

		next = listResponse.Links.Next
	}

	println("committing")
	if err := gitCommitAllAndPush(ctx, gitDir); err != nil {
		panic(err)
	}
}

func gitCommitAllAndPush(ctx context.Context, gitDir string) error {
	cmd := exec.CommandContext(ctx, "git", "add", ".")
	cmd.Dir = gitDir
	if err := cmd.Run(); err != nil {
		return err
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", fmt.Sprintf("wiki dump on %s", time.Now()))
	cmd.Dir = gitDir
	if err := cmd.Run(); err != nil {
		return err
	}

	cmd = exec.CommandContext(ctx, "git", "push", "origin", "main")
	cmd.Dir = gitDir
	if err := cmd.Run(); err != nil {
		return err
	}

	return nil
}

func listContent(ctx context.Context, url string) (*ListContentResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var payload ListContentResponse
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return nil, err
	}

	return &payload, nil
}

func handleListResponse(ctx context.Context, gitDir string, listResponse *ListContentResponse) error {
	p := pool.New().WithContext(ctx)
	for _, result := range listResponse.Results {
		result := result
		p.Go(func(ctx context.Context) error {
			return handlePage(ctx, gitDir, listResponse.Links.Base, result)
		})
	}
	return p.Wait()
}

func handlePage(ctx context.Context, gitDir string, baseURL string, result ListResult) error {
	contents, err := fetchPageContents(ctx, result)
	if err != nil {
		return err
	}

	sanitizedContents := sanitizeHTML(contents)

	return writePage(gitDir, baseURL, result, sanitizedContents)
}

func writePage(gitDir string, baseURL string, result ListResult, contents string) error {
	path := filepath.Join(gitDir, strings.TrimPrefix(baseURL+result.Links.Webui, "https://")) + ".html"
	filepath.Dir(path)

	err := os.MkdirAll(filepath.Dir(path), fs.FileMode(0755))
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(contents), fs.FileMode(0755))
}

func fetchPageContents(ctx context.Context, result ListResult) (string, error) {
	url := result.Links.Self + "?expand=body.export_view"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var payload FetchContentResponse
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return "", err
	}

	return payload.Body.ExportView.Value, nil
}

var policy = func() *bluemonday.Policy {
	bluemonday.UGCPolicy()
	p := bluemonday.NewPolicy()
	p.AllowStandardAttributes()
	p.AllowStandardURLs()
	p.AllowLists()
	p.AllowTables()
	p.AllowImages()
	p.AllowElements("section")
	p.AllowElements("summary")
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("href").OnElements("a")
	return p
}()

func sanitizeHTML(input string) string {
	return gohtml.Format(policy.Sanitize(input))
}

type FetchContentResponse struct {
	Body struct {
		ExportView struct {
			Value string `json:"value"`
		} `json:"export_view"`
	} `json:"body"`
}

type ListContentResponse struct {
	Links   ListLinks `json:"_links"`
	Limit   int
	Size    int
	Start   int
	Results []ListResult
}

type ListLinks struct {
	Base    string
	Context string
	Next    string
	Self    string
}

type ResultLinks struct {
	Self   string
	Tinyui string
	Editui string
	Webui  string
}

type ListResult struct {
	ID     string
	Type   string
	Status string
	Title  string
	Links  ResultLinks `json:"_links"`
	// Expandable
}
