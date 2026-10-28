package cli

import (
	"encoding/json"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/tweinmann/shelf/internal/cluster"
	"github.com/tweinmann/shelf/internal/ops"
)

// The documents `--json` prints are the contract with the agents that read them, so they are
// types of their own rather than the structs of ops and cluster: an internal rename must not
// reach an agent. Within one apiVersion a field is never removed, renamed or given another
// meaning; new fields may be added. Empty fields are left out, never null. Every kind has a
// golden file in testdata/json.

// documentAPIVersion is the version of every document --json prints.
const documentAPIVersion = "shelf.dev/v1alpha1"

// jsonFlag is the name of the flag, looked up again by Execute to report an error as a document.
const jsonFlag = "json"

// header starts every document.
type header struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

func newHeader(kind string) header { return header{APIVersion: documentAPIVersion, Kind: kind} }

// registerJSON adds --json to a command.
func registerJSON(cmd *cobra.Command, value *bool) {
	cmd.Flags().BoolVar(value, jsonFlag, false,
		"print one JSON document ("+documentAPIVersion+") instead of text, for scripts and agents")
}

// wantsJSON reports whether a command was run with --json.
func wantsJSON(cmd *cobra.Command) bool {
	flag := cmd.Flags().Lookup(jsonFlag)
	return flag != nil && flag.Value.String() == "true"
}

func printJSON(w io.Writer, doc any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// errorDocument is what a command run with --json prints when it fails.
type errorDocument struct {
	header
	Message string `json:"message"`
}

// appSummary is one app in an AppList.
type appSummary struct {
	Name     string        `json:"name"`
	Phase    cluster.Phase `json:"phase"`
	Stage    string        `json:"stage,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Artifact string        `json:"artifact"`
	Revision string        `json:"revision,omitempty"`
	Commit   string        `json:"commit,omitempty"`
	Host     string        `json:"host,omitempty"`
	URL      string        `json:"url,omitempty"`
	Deployed *time.Time    `json:"deployed,omitempty"`
}

type appListDocument struct {
	header
	Items []appSummary `json:"items"`
}

// exposure says how an app reaches the internet, if it does.
type exposure struct {
	// Kind is none, cloudflare or quick.
	Kind       string `json:"kind"`
	Connection string `json:"connection,omitempty"`
	Account    string `json:"account,omitempty"`
	// ConnectionMissing means this machine does not hold the app's Cloudflare connection.
	ConnectionMissing bool   `json:"connectionMissing,omitempty"`
	Domain            string `json:"domain,omitempty"`
}

type componentDocument struct {
	Name     string        `json:"name"`
	Phase    cluster.Phase `json:"phase"`
	Path     string        `json:"path,omitempty"`
	Address  string        `json:"address,omitempty"`
	URL      string        `json:"url,omitempty"`
	Internal bool          `json:"internal,omitempty"`
}

type stageDocument struct {
	Name    string        `json:"name"`
	Object  string        `json:"object"`
	Phase   cluster.Phase `json:"phase"`
	Reason  string        `json:"reason,omitempty"`
	Message string        `json:"message,omitempty"`
	Hint    string        `json:"hint,omitempty"`
}

// previewDocument is one preview in the AppStatus of its app. The cluster knows a branch by its
// slug only, so that is what names it.
type previewDocument struct {
	Slug      string        `json:"slug"`
	Tag       string        `json:"tag"`
	Namespace string        `json:"namespace"`
	Host      string        `json:"host"`
	Phase     cluster.Phase `json:"phase"`
	Commit    string        `json:"commit,omitempty"`
}

type appStatusDocument struct {
	header
	Name string `json:"name"`
	// PreviewOf is set when this is the status of a preview: the app it is a preview of.
	PreviewOf  string              `json:"previewOf,omitempty"`
	Phase      cluster.Phase       `json:"phase"`
	Host       string              `json:"host,omitempty"`
	URL        string              `json:"url,omitempty"`
	Artifact   string              `json:"artifact"`
	Insecure   bool                `json:"insecure,omitempty"`
	Registry   string              `json:"registry,omitempty"`
	Exposure   exposure            `json:"exposure"`
	Revision   string              `json:"revision,omitempty"`
	Commit     string              `json:"commit,omitempty"`
	Deployed   *time.Time          `json:"deployed,omitempty"`
	Components []componentDocument `json:"components"`
	Stages     []stageDocument     `json:"stages"`
	// Previews, PreviewLimit and PreviewLimitReached are there only for an app with previews
	// switched on.
	Previews            []previewDocument `json:"previews,omitempty"`
	PreviewLimit        int               `json:"previewLimit,omitempty"`
	PreviewLimitReached *bool             `json:"previewLimitReached,omitempty"`
	// Verification is whether the app runs what the cluster's signer signed and CI scanned.
	Verification *verificationDocument `json:"verification,omitempty"`
}

type verificationDocument struct {
	Verified bool `json:"verified"`
	// Reason is the first thing that does not hold, and Hint what to do; both only when not
	// verified.
	Reason string `json:"reason,omitempty"`
	Hint   string `json:"hint,omitempty"`
	Source string `json:"source,omitempty"`
	Signer string `json:"signer"`
	// Exposable is whether the app may get a tunnel; never for a preview.
	Exposable bool `json:"exposable"`
	// Scan is one entry per component, left out when the artifact has no scan summary.
	Scan []scanDocument `json:"scan,omitempty"`
}

type scanDocument struct {
	Component string `json:"component"`
	Digest    string `json:"digest,omitempty"`
	// Scanned is false when the summary does not name the component's image.
	Scanned   bool   `json:"scanned"`
	Critical  int    `json:"critical"`
	High      int    `json:"high"`
	Scanner   string `json:"scanner,omitempty"`
	ScannedAt string `json:"scannedAt,omitempty"`
}

type waitResultDocument struct {
	header
	// Outcome is Ready, Failed or TimedOut.
	Outcome  ops.Outcome `json:"outcome"`
	App      string      `json:"app"`
	Commit   string      `json:"commit,omitempty"`
	Revision string      `json:"revision,omitempty"`
	// Stage is the first stage that is not Ready; it is left out when the outcome is Ready.
	Stage *stageDocument `json:"stage,omitempty"`
}

func waitResult(r ops.WaitResult) waitResultDocument {
	doc := waitResultDocument{header: newHeader("WaitResult"), Outcome: r.Outcome, App: r.App,
		Commit: r.Commit, Revision: r.Revision}
	if r.Stage != nil {
		s := stage(*r.Stage)
		doc.Stage = &s
	}
	return doc
}

type tagDocument struct {
	Name    string     `json:"name"`
	Created *time.Time `json:"created,omitempty"`
	Current bool       `json:"current,omitempty"`
}

type tagListDocument struct {
	header
	App   string        `json:"app"`
	Items []tagDocument `json:"items"`
}

type connectionDocument struct {
	// Kind is registry or cloudflare.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// User and Registry belong to a registry connection.
	User     string `json:"user,omitempty"`
	Registry string `json:"registry,omitempty"`
	// Account belongs to a Cloudflare connection; Missing means this machine does not hold it.
	Account string   `json:"account,omitempty"`
	Missing bool     `json:"missing,omitempty"`
	Apps    []string `json:"apps"`
}

type connectionListDocument struct {
	header
	Items []connectionDocument `json:"items"`
}

type versionDocument struct {
	header
	Version string `json:"version"`
	// AppAPIVersion is the apiVersion of the app.yaml files this binary reads.
	AppAPIVersion string `json:"appAPIVersion"`
}

// timestamp is a time in UTC, or nil for the zero time, so that the field is left out.
func timestamp(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

func appList(apps []ops.App) appListDocument {
	doc := appListDocument{header: newHeader("AppList"), Items: []appSummary{}}
	for _, a := range apps {
		doc.Items = append(doc.Items, appSummary{
			Name: a.Name, Phase: a.Phase, Stage: a.Stage, Reason: a.Reason,
			Artifact: a.Artifact.String(), Revision: a.Revision, Commit: a.Commit,
			Host: a.Host, URL: a.URL, Deployed: timestamp(a.Deployed),
		})
	}
	return doc
}

func appStatus(a ops.App, d cluster.Diagnosis) appStatusDocument {
	doc := appStatusDocument{
		header: newHeader("AppStatus"),
		Name:   a.Name, Phase: a.Phase, Host: a.Host, URL: a.URL,
		Artifact: a.Artifact.String(), Insecure: a.Insecure, Registry: a.Registry,
		Exposure: exposure{Kind: "none"},
		Revision: a.Revision, Commit: a.Commit, Deployed: timestamp(a.Deployed),
		Components: []componentDocument{},
		Stages:     stages(d),
	}
	switch {
	case a.Cloudflare != "":
		doc.Exposure = exposure{Kind: "cloudflare", Connection: a.Cloudflare, Account: a.CloudflareAccount,
			ConnectionMissing: a.CloudflareMissing, Domain: a.Domain}
	case a.Quick:
		doc.Exposure = exposure{Kind: "quick"}
	}
	if a.PreviewLimit > 0 {
		doc.PreviewLimit = a.PreviewLimit
		reached := len(a.Previews) >= a.PreviewLimit
		doc.PreviewLimitReached = &reached
		doc.Previews = []previewDocument{}
		for _, p := range a.Previews {
			doc.Previews = append(doc.Previews, previewDocument{Slug: p.Slug, Tag: p.Artifact.Tag,
				Namespace: p.Name, Host: ops.PreviewHost(p.Name), Phase: p.Phase, Commit: p.Commit})
		}
	}
	for _, c := range a.Components {
		doc.Components = append(doc.Components, componentDocument{
			Name: c.Name, Phase: c.Phase, Path: c.Path, Address: c.Address, URL: c.URL, Internal: c.Internal,
		})
	}
	if v := a.Verification; v != nil {
		doc.Verification = &verificationDocument{Verified: v.Verified, Reason: v.Reason, Hint: v.Hint,
			Source: v.Source, Signer: v.Signer, Exposable: v.Exposable}
		if v.Scan != nil {
			for _, img := range v.Scan.Images {
				doc.Verification.Scan = append(doc.Verification.Scan, scanDocument{Component: img.Component,
					Digest: img.Digest, Scanned: img.Scanned, Critical: img.Critical, High: img.High,
					Scanner: v.Scan.Scanner, ScannedAt: v.Scan.ScannedAt})
			}
		}
	}
	return doc
}

func stages(d cluster.Diagnosis) []stageDocument {
	out := make([]stageDocument, 0, len(d.Stages))
	for _, s := range d.Stages {
		out = append(out, stage(s))
	}
	return out
}

func stage(s cluster.Stage) stageDocument {
	return stageDocument{Name: s.Name, Object: s.Object, Phase: s.Phase, Reason: s.Reason,
		Message: s.Message, Hint: s.Hint}
}

func tagList(app string, current string, tags []ops.Tag) tagListDocument {
	doc := tagListDocument{header: newHeader("TagList"), App: app, Items: []tagDocument{}}
	for _, t := range tags {
		doc.Items = append(doc.Items, tagDocument{Name: t.Name, Created: timestamp(t.Created), Current: t.Name == current})
	}
	return doc
}

func connectionList(conns ops.Connections) connectionListDocument {
	doc := connectionListDocument{header: newHeader("ConnectionList"), Items: []connectionDocument{}}
	for _, c := range conns.Registry {
		doc.Items = append(doc.Items, connectionDocument{Kind: "registry", Name: c.Name, User: c.Username,
			Registry: cluster.RegistryHost, Apps: orEmpty(c.Apps)})
	}
	for _, c := range conns.Cloudflare {
		doc.Items = append(doc.Items, connectionDocument{Kind: "cloudflare", Name: c.Name, Account: c.Account,
			Missing: c.Missing, Apps: orEmpty(c.Apps)})
	}
	return doc
}

// orEmpty keeps a list a list in JSON: an unused connection has "apps": [], not null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
