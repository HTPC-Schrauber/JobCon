package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

func NewClient(baseURL, username, password string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Username: username,
		Password: password,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type Component struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Group      string `json:"group"`
	Name       string `json:"name"`
	Version    string `json:"version"`
}

type searchResponse struct {
	Items             []Component `json:"items"`
	ContinuationToken *string     `json:"continuationToken"`
}

type ArtifactNode struct {
	ArtifactID string   `json:"artifact_id"`
	Group      string   `json:"group"`
	Versions   []string `json:"versions"`
}

type GroupNode struct {
	Group     string         `json:"group"`
	Artifacts []ArtifactNode `json:"artifacts"`
}

// TestConnection checks if Nexus is reachable and credentials are valid
func (c *Client) TestConnection(ctx context.Context) (bool, string, error) {
	if c.BaseURL == "" {
		return false, "Nexus Basis-URL ist nicht konfiguriert", nil
	}

	// First try repositories endpoint which tests both reachability and auth
	testURL := fmt.Sprintf("%s/service/rest/v1/repositories", c.BaseURL)
	req, err := http.NewRequestWithContext(ctx, "GET", testURL, nil)
	if err != nil {
		return false, "", err
	}

	if c.Username != "" && c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// If /service/rest/v1/repositories failed, try simple HEAD to base URL
		headReq, headErr := http.NewRequestWithContext(ctx, "HEAD", c.BaseURL, nil)
		if headErr == nil {
			if headResp, err2 := c.HTTPClient.Do(headReq); err2 == nil {
				_ = headResp.Body.Close()
				return true, fmt.Sprintf("Nexus Server erreichbar (HTTP %d, REST-API nicht aktiv)", headResp.StatusCode), nil
			}
		}
		return false, fmt.Sprintf("Verbindung fehlgeschlagen: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		if c.Username == "" {
			return true, "Verbindung erfolgreich (anonymer Zugriff)", nil
		}
		return true, "Verbindung und Authentifizierung erfolgreich", nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Sprintf("Authentifizierung fehlgeschlagen (HTTP %d)", resp.StatusCode), nil
	}

	return false, fmt.Sprintf("Unerwartete Antwort von Nexus (HTTP %d)", resp.StatusCode), nil
}

// sanitizeNexusQuery cleans search queries for Nexus 3 REST search API.
// Nexus Elasticsearch throws HTTP 400 on leading wildcards (*, ?) or unbalanced quotes.
func sanitizeNexusQuery(q string) string {
	q = strings.TrimSpace(q)
	// Remove double quotes which could cause Elasticsearch Lucene syntax errors
	q = strings.ReplaceAll(q, "\"", " ")
	// Remove leading wildcards
	q = strings.TrimLeft(q, "*? \t")
	// If query looks like a version badge (e.g. "v1.2.0" or "v2"), strip leading 'v' / 'V'
	if len(q) > 1 && (q[0] == 'v' || q[0] == 'V') && (q[1] >= '0' && q[1] <= '9') {
		q = q[1:]
	}
	return strings.TrimSpace(q)
}

// SearchComponents queries Nexus REST API for Maven components
func (c *Client) SearchComponents(ctx context.Context, repository, groupFilter, query string) ([]Component, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("nexus base_url is not configured")
	}

	cleanQuery := sanitizeNexusQuery(query)
	cleanGroup := strings.TrimSpace(groupFilter)
	cleanGroup = strings.TrimLeft(cleanGroup, "*? \t")

	endpoint := fmt.Sprintf("%s/service/rest/v1/search", c.BaseURL)

	var allComponents []Component
	var continuationToken *string

	// Paginate through results (up to 20 pages / 1000 items max)
	for page := 0; page < 20; page++ {
		params := url.Values{}
		if repository != "" {
			params.Set("repository", repository)
		}
		if cleanGroup != "" {
			if !strings.HasSuffix(cleanGroup, "*") {
				params.Set("group", cleanGroup+"*")
			} else {
				params.Set("group", cleanGroup)
			}
		}
		if cleanQuery != "" {
			params.Set("q", cleanQuery)
		}
		if continuationToken != nil && *continuationToken != "" {
			params.Set("continuationToken", *continuationToken)
		}

		searchURL := fmt.Sprintf("%s?%s", endpoint, params.Encode())
		req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
		if err != nil {
			return nil, err
		}

		if c.Username != "" && c.Password != "" {
			req.SetBasicAuth(c.Username, c.Password)
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()

			var siestaErrors []struct {
				ID      string `json:"id"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(bodyBytes, &siestaErrors); err == nil && len(siestaErrors) > 0 {
				var msgs []string
				for _, se := range siestaErrors {
					if se.Message != "" {
						msgs = append(msgs, se.Message)
					}
				}
				if len(msgs) > 0 {
					return nil, fmt.Errorf("nexus returned HTTP %d: %s", resp.StatusCode, strings.Join(msgs, ", "))
				}
			}
			errMsg := strings.TrimSpace(string(bodyBytes))
			if errMsg != "" {
				return nil, fmt.Errorf("nexus returned HTTP %d: %s", resp.StatusCode, errMsg)
			}
			return nil, fmt.Errorf("nexus returned HTTP %d", resp.StatusCode)
		}

		var res searchResponse
		err = json.NewDecoder(resp.Body).Decode(&res)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}

		allComponents = append(allComponents, res.Items...)

		if res.ContinuationToken == nil || *res.ContinuationToken == "" {
			break
		}
		continuationToken = res.ContinuationToken
	}

	return allComponents, nil
}

// BrowseTree groups components by Group -> Artifact -> Versions for hierarchical UI picker
func (c *Client) BrowseTree(ctx context.Context, repository, groupFilter, search string) ([]GroupNode, error) {
	components, err := c.SearchComponents(ctx, repository, groupFilter, search)
	if err != nil {
		return nil, err
	}

	// Map: group -> artifact -> set of versions
	treeMap := make(map[string]map[string]map[string]struct{})
	for _, comp := range components {
		g := comp.Group
		if g == "" {
			g = "default"
		}
		a := comp.Name
		v := comp.Version
		if a == "" {
			continue
		}

		if _, exists := treeMap[g]; !exists {
			treeMap[g] = make(map[string]map[string]struct{})
		}
		if _, exists := treeMap[g][a]; !exists {
			treeMap[g][a] = make(map[string]struct{})
		}
		if v != "" {
			treeMap[g][a][v] = struct{}{}
		}
	}

	var groupNodes []GroupNode
	for groupName, artMap := range treeMap {
		var artifacts []ArtifactNode
		for artName, verSet := range artMap {
			var versions []string
			for ver := range verSet {
				versions = append(versions, ver)
			}
			// Sort versions descending (newest first)
			sort.Slice(versions, func(i, j int) bool {
				return versions[i] > versions[j]
			})

			artifacts = append(artifacts, ArtifactNode{
				ArtifactID: artName,
				Group:      groupName,
				Versions:   versions,
			})
		}

		sort.Slice(artifacts, func(i, j int) bool {
			return artifacts[i].ArtifactID < artifacts[j].ArtifactID
		})

		groupNodes = append(groupNodes, GroupNode{
			Group:     groupName,
			Artifacts: artifacts,
		})
	}

	sort.Slice(groupNodes, func(i, j int) bool {
		return groupNodes[i].Group < groupNodes[j].Group
	})

	return groupNodes, nil
}
