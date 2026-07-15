package policy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// EvaluationSchemaVersion is the additive profile-evaluation wire contract.
const EvaluationSchemaVersion = 1

// Closed v1 enums. Unknown values are represented by the explicit unknown
// members; callers must not interpret an unrecognized string as bounded/pass.
const (
	FindingAxisNetwork     = "network"
	FindingAxisFiles       = "files"
	FindingAxisProjection  = "projection"
	FindingAxisSecrets     = "secrets"
	FindingAxisCredentials = "credentials"
	FindingAxisTrust       = "trust"
	FindingAxisReadiness   = "readiness"

	FindingOutcomeConcern       = "concern"
	FindingOutcomeBounded       = "bounded"
	FindingOutcomePass          = "pass"
	FindingOutcomeFail          = "fail"
	FindingOutcomeUnknown       = "unknown"
	FindingOutcomeNotApplicable = "not_applicable"

	FindingSeverityCritical = "critical"
	FindingSeverityHigh     = "high"
	FindingSeverityMedium   = "medium"
	FindingSeverityInfo     = "info"

	TrustStateTrusted       = "trusted"
	TrustStateUntrusted     = "untrusted"
	TrustStateChanged       = "changed"
	TrustStateUnknown       = "unknown"
	TrustStateNotApplicable = "not_applicable"

	TrustBasisProjectExactBytes = "project_exact_bytes"
	TrustBasisEmbeddedBuiltin   = "embedded_builtin"
	TrustBasisUnsaved           = "unsaved"
	TrustBasisUnknown           = "unknown"

	ReadinessStateReady         = "ready"
	ReadinessStateBlocked       = "blocked"
	ReadinessStateUnknown       = "unknown"
	ReadinessStateNotApplicable = "not_applicable"

	RemediationKindPolicyChange           = "policy_change"
	RemediationKindOperatorWorkflow       = "operator_workflow"
	RemediationKindInstallHelper          = "install_helper"
	RemediationKindRepairHelperResolution = "repair_helper_resolution"
	RemediationKindReviewAndTrust         = "review_and_trust"
	RemediationKindRetryCheck             = "retry_check"

	CredentialProviderPnpm    = "pnpm"
	CredentialProviderAWS     = "aws"
	CredentialProviderGCP     = "gcp"
	CredentialProviderKube    = "kube"
	CredentialProviderGitHub  = "github"
	CredentialProviderForgejo = "forgejo"

	CredentialAccessReadOnly        = "read_only"
	CredentialAccessReadWrite       = "read_write"
	CredentialAccessScopedAPI       = "scoped_api"
	CredentialAccessExternalPolicy  = "external_policy"
	CredentialAccessProviderDefault = "provider_default"
	CredentialAccessUnknown         = "unknown"

	CredentialLifetimeShortLived = "short_lived"
	CredentialLifetimePersistent = "persistent"
	CredentialLifetimeUnknown    = "unknown"

	CredentialBasisDeclared         = "declared"
	CredentialBasisResolvedAtLaunch = "resolved_at_launch"
	CredentialBasisProviderDefault  = "provider_default"
)

// Evaluation keeps static authority, exact-policy trust, and point-in-time
// readiness structurally separate. There is deliberately no aggregate verdict.
type Evaluation struct {
	SchemaVersion int                 `json:"schema_version"`
	Authority     AuthorityEvaluation `json:"authority"`
	Trust         TrustEvaluation     `json:"trust"`
	Readiness     ReadinessEvaluation `json:"readiness"`
}

type AuthorityEvaluation struct {
	Findings         []Finding         `json:"findings"`
	CredentialScopes []CredentialScope `json:"credential_scopes"`
}

type TrustEvaluation struct {
	State     string     `json:"state"`
	Basis     string     `json:"basis"`
	CheckedAt *time.Time `json:"checked_at"`
	Findings  []Finding  `json:"findings"`
}

type ReadinessEvaluation struct {
	State     string     `json:"state"`
	CheckedAt *time.Time `json:"checked_at"`
	Findings  []Finding  `json:"findings"`
}

type Finding struct {
	RuleID      string       `json:"rule_id"`
	Axis        string       `json:"axis"`
	Outcome     string       `json:"outcome"`
	Severity    string       `json:"severity"`
	Title       string       `json:"title"`
	Consequence string       `json:"consequence"`
	ScopeIDs    []string     `json:"scope_ids"`
	Remediation *Remediation `json:"remediation"`
}

type Remediation struct {
	Kind     string `json:"kind"`
	ActionID string `json:"action_id"`
	Summary  string `json:"summary"`
	DocsRef  string `json:"docs_ref"`
}

type CredentialScope struct {
	ScopeID  string `json:"scope_id"`
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Access   string `json:"access"`
	Lifetime string `json:"lifetime"`
	Basis    string `json:"basis"`
}

// The custom marshalers enforce the v1 array law even for a zero-value context
// section assembled by a later layer. Nullable pointers remain explicit nulls.
func (a AuthorityEvaluation) MarshalJSON() ([]byte, error) {
	type wire AuthorityEvaluation
	if a.Findings == nil {
		a.Findings = []Finding{}
	}
	if a.CredentialScopes == nil {
		a.CredentialScopes = []CredentialScope{}
	}
	return json.Marshal(wire(a))
}

func (t TrustEvaluation) MarshalJSON() ([]byte, error) {
	type wire TrustEvaluation
	if t.Findings == nil {
		t.Findings = []Finding{}
	}
	return json.Marshal(wire(t))
}

func (r ReadinessEvaluation) MarshalJSON() ([]byte, error) {
	type wire ReadinessEvaluation
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return json.Marshal(wire(r))
}

func (f Finding) MarshalJSON() ([]byte, error) {
	type wire Finding
	if f.ScopeIDs == nil {
		f.ScopeIDs = []string{}
	}
	return json.Marshal(wire(f))
}

type findingDisposition struct {
	Outcome  string
	Severity string
}

type authorityRuleContext struct {
	Count        int
	Destinations []string
	Write        bool
	Persistent   bool
	Unknown      bool
}

type authorityNetworkReach uint8

type authorityFileReach uint8

type authorityProjectionReach uint8

const (
	authorityNetworkUnknown authorityNetworkReach = iota
	authorityNetworkHostUnrestricted
	authorityNetworkContainerOpen
	authorityNetworkContainerAllowlisted
)

const (
	authorityFilesUnknown authorityFileReach = iota
	authorityFilesHostAccount
	authorityFilesWorkspace
)

const (
	authorityProjectionUnknown authorityProjectionReach = iota
	authorityProjectionLiveHostConfig
	authorityProjectionAbsent
	authorityProjectionNotApplicable
)

// normalizedAuthorityFacts is the single decoded-policy authority model used by
// evaluation and the legacy risk/lint compatibility projections. Presentation
// layers may retain old prose, but they must not re-decide these predicates.
type normalizedAuthorityFacts struct {
	Network                 authorityNetworkReach
	OpenEgress              bool
	Files                   authorityFileReach
	Projection              authorityProjectionReach
	ProjectionLabels        []string
	SecretNames             []string
	EgressDestinations      []string
	EgressDestinationsValid bool
	EgressCount             int
	EgressIgnored           bool
	CredentialScopes        []CredentialScope
	CredentialProviders     []credentialProviderAuthority
}

func normalizeAuthorityFacts(p Profile) normalizedAuthorityFacts {
	facts := normalizedAuthorityFacts{}

	switch {
	case p.Environment == "host":
		facts.Network = authorityNetworkHostUnrestricted
	case p.Environment == "container" && p.Network == "allow":
		facts.Network = authorityNetworkContainerOpen
	case p.Environment == "container" && p.Network == "deny":
		facts.Network = authorityNetworkContainerAllowlisted
	default:
		facts.Network = authorityNetworkUnknown
	}

	facts.OpenEgress = p.Environment == "host" || p.Network == "allow"

	switch p.Environment {
	case "host":
		facts.Files = authorityFilesHostAccount
	case "container":
		facts.Files = authorityFilesWorkspace
	default:
		facts.Files = authorityFilesUnknown
	}

	switch {
	case p.Environment == "host":
		facts.Projection = authorityProjectionNotApplicable
	case p.Environment != "container":
		facts.Projection = authorityProjectionUnknown
	case p.Projection == nil || (!p.Projection.Enabled && len(p.Projection.Items) == 0):
		facts.Projection = authorityProjectionAbsent
	case p.Projection.Enabled && len(p.Projection.Items) > 0:
		facts.Projection = authorityProjectionLiveHostConfig
		facts.ProjectionLabels = make([]string, 0, len(p.Projection.Items))
		for _, item := range p.Projection.Items {
			label := item.Label
			if label == "" {
				label = item.Source
			}
			facts.ProjectionLabels = append(facts.ProjectionLabels, label)
		}
		sort.Strings(facts.ProjectionLabels)
	default:
		facts.Projection = authorityProjectionUnknown
	}

	facts.SecretNames = make([]string, 0, len(p.Secrets))
	for name := range p.Secrets {
		facts.SecretNames = append(facts.SecretNames, name)
	}
	sort.Strings(facts.SecretNames)

	facts.EgressDestinations, facts.EgressDestinationsValid = valueFreeEgressDestinations(p.Egress)
	facts.EgressCount = len(p.Egress)
	facts.EgressIgnored = facts.EgressCount > 0 && facts.Network != authorityNetworkContainerAllowlisted
	facts.CredentialScopes, facts.CredentialProviders = normalizeCredentialAuthority(p.Credentials)
	return facts
}

func (f normalizedAuthorityFacts) hasOpenEgress() bool {
	return f.OpenEgress
}

func (f normalizedAuthorityFacts) providerHasWrite(provider string) bool {
	for _, credentialProvider := range f.CredentialProviders {
		if credentialProvider.Provider == provider {
			return len(credentialProvider.WriteScopeIDs) > 0
		}
	}
	return false
}

// compatibilityRuleApplies owns the settled predicates shared by evaluation
// findings and legacy lint codes. Rule IDs retain their existing meaning.
func (f normalizedAuthorityFacts) compatibilityRuleApplies(ruleID string) bool {
	switch ruleID {
	case "github-write-open-egress":
		return f.hasOpenEgress() && f.providerHasWrite(CredentialProviderGitHub)
	case "forgejo-write-open-egress":
		return f.hasOpenEgress() && f.providerHasWrite(CredentialProviderForgejo)
	case "egress-ignored":
		return f.EgressIgnored
	default:
		return false
	}
}

// authorityRule is the engine-owned identity and presentation registry. A rule
// owns its axis, legal dispositions, consequence builder, remediation policy,
// docs anchor, and stable ordinal; profile data never participates in its ID.
type authorityRule struct {
	ID          string
	Axis        string
	Title       string
	Ordinal     int
	Allowed     []findingDisposition
	Consequence func(authorityRuleContext) string
	Remediation *Remediation
}

const evaluationDocs = "specs/0101-profile-safety-evaluation.md"

var authorityRuleRegistry = []authorityRule{
	{
		ID: "authority.network.host-unrestricted", Axis: FindingAxisNetwork, Ordinal: 100,
		Title:       "Host network is unrestricted",
		Allowed:     []findingDisposition{{FindingOutcomeConcern, FindingSeverityCritical}},
		Consequence: fixedConsequence("A compromised run uses the full host network and can send data to any destination reachable by the account."),
		Remediation: authorityRemediation("use-container-deny", "Use a container profile with network deny for enforceable egress limits.", "#authority-network"),
	},
	{
		ID: "authority.network.container-open-egress", Axis: FindingAxisNetwork, Ordinal: 110,
		Title:       "Container egress is open",
		Allowed:     []findingDisposition{{FindingOutcomeConcern, FindingSeverityHigh}},
		Consequence: fixedConsequence("The container limits host file reach, but a compromised run can send reachable data to arbitrary internet destinations."),
		Remediation: authorityRemediation("set-network-deny", "Set network to deny and keep only reviewed allowlist destinations.", "#authority-network"),
	},
	{
		ID: "authority.network.unknown", Axis: FindingAxisNetwork, Ordinal: 120,
		Title:       "Network authority is unknown",
		Allowed:     []findingDisposition{{FindingOutcomeUnknown, FindingSeverityCritical}},
		Consequence: fixedConsequence("The environment or network mode is not recognized; assume a compromised run can reach unrestricted network destinations."),
		Remediation: authorityRemediation("review-network-policy", "Choose a recognized environment and explicit network mode before launch.", "#authority-network"),
	},
	{
		ID: "egress-ignored", Axis: FindingAxisNetwork, Ordinal: 130,
		Title:   "Configured egress destinations are ignored",
		Allowed: []findingDisposition{{FindingOutcomeConcern, FindingSeverityMedium}},
		Consequence: func(c authorityRuleContext) string {
			return fmt.Sprintf("%s configured egress destination does not constrain actual reach because allowlists apply only to container profiles with network deny.", countNoun(c.Count, "egress destination"))
		},
		Remediation: authorityRemediation("make-egress-enforceable", "Use a container with network deny, or remove the ineffective egress declaration.", "#authority-network"),
	},
	{
		ID: "authority.network.container-allowlist", Axis: FindingAxisNetwork, Ordinal: 140,
		Title:   "Container egress is allowlisted",
		Allowed: []findingDisposition{{FindingOutcomeBounded, FindingSeverityInfo}},
		Consequence: func(c authorityRuleContext) string {
			if len(c.Destinations) == 0 {
				return "Egress is limited to engine, agent, and resolved-package allowlist destinations; every allowed destination remains a possible exfiltration channel."
			}
			return "Egress is allowlisted; declared destinations " + strings.Join(c.Destinations, ", ") + " remain possible exfiltration channels."
		},
	},
	{
		ID: "authority.files.host-account", Axis: FindingAxisFiles, Ordinal: 200,
		Title:       "Host account files are reachable",
		Allowed:     []findingDisposition{{FindingOutcomeConcern, FindingSeverityCritical}},
		Consequence: fixedConsequence("A compromised run executes as the user and can read or modify the whole account, including any file the account can access."),
		Remediation: authorityRemediation("use-workspace-boundary", "Use a container profile so writable host reach is limited to the workspace.", "#authority-files"),
	},
	{
		ID: "authority.files.unknown", Axis: FindingAxisFiles, Ordinal: 210,
		Title:       "Host file reach is unknown",
		Allowed:     []findingDisposition{{FindingOutcomeUnknown, FindingSeverityCritical}},
		Consequence: fixedConsequence("The environment is not recognized; conservatively assume the whole host account is readable and writable."),
		Remediation: authorityRemediation("review-file-boundary", "Choose a recognized environment with an explicit file boundary before launch.", "#authority-files"),
	},
	{
		ID: "authority.files.workspace", Axis: FindingAxisFiles, Ordinal: 220,
		Title:       "Workspace is readable and writable",
		Allowed:     []findingDisposition{{FindingOutcomeBounded, FindingSeverityInfo}},
		Consequence: fixedConsequence("The container can read and modify the mounted workspace; other host config is reachable only when separately reported as a projection."),
	},
	{
		ID: "authority.projection.live-host-config", Axis: FindingAxisProjection, Ordinal: 300,
		Title:   "Live host config is projected",
		Allowed: []findingDisposition{{FindingOutcomeConcern, FindingSeverityMedium}},
		Consequence: func(c authorityRuleContext) string {
			return fmt.Sprintf("%s is copied read-only into the ephemeral home; its readable instruction or configuration authority is not content-pinned by the policy hash.", countNoun(c.Count, "projected live host config item"))
		},
		Remediation: authorityRemediation("review-builtin-projection", "Review the builtin projection or choose a profile without projected host config.", "#authority-projection"),
	},
	{
		ID: "authority.projection.unknown", Axis: FindingAxisProjection, Ordinal: 310,
		Title:       "Projection authority is unknown",
		Allowed:     []findingDisposition{{FindingOutcomeUnknown, FindingSeverityHigh}},
		Consequence: fixedConsequence("The projection shape is inconsistent with the engine-owned model; assume additional live host configuration may be readable."),
		Remediation: authorityRemediation("review-projection-policy", "Use a recognized engine-owned projection before launch.", "#authority-projection"),
	},
	{
		ID: "authority.projection.absent", Axis: FindingAxisProjection, Ordinal: 320,
		Title:       "No host config projection",
		Allowed:     []findingDisposition{{FindingOutcomeBounded, FindingSeverityInfo}},
		Consequence: fixedConsequence("No engine-owned host configuration is projected into the container beyond the separately reported workspace mount."),
	},
	{
		ID: "authority.projection.not-applicable", Axis: FindingAxisProjection, Ordinal: 330,
		Title:       "Projection boundary is not applicable",
		Allowed:     []findingDisposition{{FindingOutcomeNotApplicable, FindingSeverityInfo}},
		Consequence: fixedConsequence("A host run already has whole-account file reach, so a separate read-only projection does not bound its authority."),
	},
	{
		ID: "authority.secrets.injected", Axis: FindingAxisSecrets, Ordinal: 400,
		Title:   "Secrets are injected",
		Allowed: []findingDisposition{{FindingOutcomeConcern, FindingSeverityHigh}},
		Consequence: func(c authorityRuleContext) string {
			return fmt.Sprintf("%s injected secret is readable by the run and can be used or exfiltrated within its network authority; names, references, and values are withheld.", countNoun(c.Count, "secret"))
		},
		Remediation: authorityRemediation("reduce-secret-injection", "Remove secrets the run does not require or use a narrower dedicated credential.", "#authority-secrets"),
	},
	{
		ID: "authority.secrets.absent", Axis: FindingAxisSecrets, Ordinal: 410,
		Title:       "No declared secret injection",
		Allowed:     []findingDisposition{{FindingOutcomeBounded, FindingSeverityInfo}},
		Consequence: fixedConsequence("The profile declares no direct secret injection; credential providers are evaluated separately."),
	},
	{
		ID: "github-write-open-egress", Axis: FindingAxisCredentials, Ordinal: 500,
		Title:       "GitHub write authority has unrestricted egress",
		Allowed:     []findingDisposition{{FindingOutcomeConcern, FindingSeverityCritical}},
		Consequence: fixedConsequence("A compromised run can exfiltrate a write-capable GitHub credential and use it outside the boundary against the linked repository scope."),
		Remediation: authorityRemediation("bound-github-write-egress", "Use network deny with reviewed forge destinations, or make every GitHub repository scope read-only.", "#authority-credentials"),
	},
	{
		ID: "forgejo-write-open-egress", Axis: FindingAxisCredentials, Ordinal: 510,
		Title:       "Forgejo write authority has unrestricted egress",
		Allowed:     []findingDisposition{{FindingOutcomeConcern, FindingSeverityCritical}},
		Consequence: fixedConsequence("A compromised run can exfiltrate a write-capable Forgejo credential and use it outside the boundary against the linked repository scope."),
		Remediation: authorityRemediation("bound-forgejo-write-egress", "Use network deny with reviewed forge destinations, or make every Forgejo repository scope read-only.", "#authority-credentials"),
	},
	credentialRule("pnpm", 520, "Registry credential authority", func(c authorityRuleContext) string {
		phrase, verb := credentialScopePhrase(c.Count, "persistent registry")
		return fmt.Sprintf("%s %s available to the run; registry policy controls whether it can read, publish, or administer packages.", phrase, verb)
	}),
	credentialRule("aws", 530, "AWS credential authority", func(c authorityRuleContext) string {
		phrase, verb := credentialScopePhrase(c.Count, "short-lived AWS")
		return fmt.Sprintf("%s %s resolved at launch; effective permissions remain controlled by external IAM policy.", phrase, verb)
	}),
	credentialRule("gcp", 540, "GCP credential authority", func(c authorityRuleContext) string {
		phrase, verb := credentialScopePhrase(c.Count, "short-lived GCP")
		return fmt.Sprintf("%s %s available to the run; declared API scopes or provider defaults bound only part of effective cloud authority.", phrase, verb)
	}),
	credentialRule("kube", 550, "Kubernetes credential authority", func(c authorityRuleContext) string {
		phrase, verb := credentialScopePhrase(c.Count, "short-lived cluster")
		return fmt.Sprintf("%s %s available to the run; effective actions remain controlled by cluster RBAC.", phrase, verb)
	}),
	credentialRule("github", 560, "GitHub credential authority", forgeCredentialConsequence("GitHub")),
	credentialRule("forgejo", 570, "Forgejo credential authority", forgeCredentialConsequence("Forgejo")),
	{
		ID: "authority.credentials.absent", Axis: FindingAxisCredentials, Ordinal: 590,
		Title:       "No credential providers declared",
		Allowed:     []findingDisposition{{FindingOutcomeBounded, FindingSeverityInfo}},
		Consequence: fixedConsequence("The profile declares no staged credential provider; direct secret injection is evaluated separately."),
	},
}

var authorityRulesByID = func() map[string]authorityRule {
	out := make(map[string]authorityRule, len(authorityRuleRegistry))
	for _, rule := range authorityRuleRegistry {
		out[rule.ID] = rule
	}
	return out
}()

func init() {
	if err := ValidateFindingRegistry(); err != nil {
		panic("invalid authority finding registry: " + err.Error())
	}
}

func fixedConsequence(text string) func(authorityRuleContext) string {
	return func(authorityRuleContext) string { return text }
}

func authorityRemediation(actionID, summary, anchor string) *Remediation {
	return &Remediation{
		Kind:     RemediationKindPolicyChange,
		ActionID: actionID,
		Summary:  summary,
		DocsRef:  evaluationDocs + anchor,
	}
}

func credentialRule(provider string, ordinal int, title string, consequence func(authorityRuleContext) string) authorityRule {
	return authorityRule{
		ID: "authority.credentials." + provider, Axis: FindingAxisCredentials, Ordinal: ordinal,
		Title: title,
		Allowed: []findingDisposition{
			{FindingOutcomeConcern, FindingSeverityMedium},
			{FindingOutcomeConcern, FindingSeverityHigh},
			{FindingOutcomeUnknown, FindingSeverityHigh},
		},
		Consequence: consequence,
		Remediation: authorityRemediation(
			"reduce-"+provider+"-credential-scope",
			"Remove this provider when unnecessary or narrow its declared target and access.",
			"#authority-credentials",
		),
	}
}

func forgeCredentialConsequence(label string) func(authorityRuleContext) string {
	return func(c authorityRuleContext) string {
		lifetime := "short-lived"
		if c.Persistent {
			lifetime = "persistent"
		}
		phrase, verb := credentialScopePhrase(c.Count, lifetime+" "+label)
		if c.Unknown {
			return fmt.Sprintf("%s %s present, but malformed or future metadata prevents a complete authority classification.", phrase, verb)
		}
		if c.Write {
			return fmt.Sprintf("%s %s available; at least one repository grants effective write access.", phrase, verb)
		}
		return fmt.Sprintf("%s %s declared read-only.", phrase, verb)
	}
}

func credentialScopePhrase(count int, qualifier string) (phrase, verb string) {
	if count == 1 {
		return "1 " + qualifier + " credential scope", "is"
	}
	return fmt.Sprintf("%d %s credential scopes", count, qualifier), "are"
}

func countNoun(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// EvaluateAuthority is intentionally pure. It reads only the decoded profile:
// no clock, environment, filesystem, trust/account store, helper, or network.
func EvaluateAuthority(p Profile) AuthorityEvaluation {
	facts := normalizeAuthorityFacts(p)
	findings := make([]Finding, 0, 10)

	findings = append(findings, evaluateNetworkAuthority(facts)...)
	findings = append(findings, evaluateFileAuthority(facts)...)
	findings = append(findings, evaluateProjectionAuthority(facts)...)
	findings = append(findings, evaluateSecretAuthority(facts)...)

	if len(facts.CredentialProviders) == 0 {
		findings = append(findings, makeAuthorityFinding(
			"authority.credentials.absent",
			FindingOutcomeBounded,
			FindingSeverityInfo,
			authorityRuleContext{},
			nil,
		))
	} else {
		for _, provider := range facts.CredentialProviders {
			combinationID := ""
			switch provider.Provider {
			case CredentialProviderGitHub:
				combinationID = "github-write-open-egress"
			case CredentialProviderForgejo:
				combinationID = "forgejo-write-open-egress"
			}
			if combinationID != "" && facts.compatibilityRuleApplies(combinationID) {
				findings = append(findings, makeAuthorityFinding(
					combinationID,
					FindingOutcomeConcern,
					FindingSeverityCritical,
					authorityRuleContext{},
					provider.WriteScopeIDs,
				))
			}

			outcome := FindingOutcomeConcern
			severity := FindingSeverityMedium
			if provider.Unknown {
				outcome = FindingOutcomeUnknown
				severity = FindingSeverityHigh
			} else if provider.HighAuthority {
				severity = FindingSeverityHigh
			}
			findings = append(findings, makeAuthorityFinding(
				"authority.credentials."+provider.Provider,
				outcome,
				severity,
				authorityRuleContext{
					Count:      len(provider.ScopeIDs),
					Write:      len(provider.WriteScopeIDs) > 0,
					Persistent: provider.Persistent,
					Unknown:    provider.Unknown,
				},
				provider.ScopeIDs,
			))
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		return authorityFindingLess(findings[i], findings[j])
	})
	return AuthorityEvaluation{
		Findings:         findings,
		CredentialScopes: facts.CredentialScopes,
	}
}

func evaluateNetworkAuthority(facts normalizedAuthorityFacts) []Finding {
	findings := make([]Finding, 0, 2)
	switch facts.Network {
	case authorityNetworkHostUnrestricted:
		findings = append(findings, makeAuthorityFinding(
			"authority.network.host-unrestricted",
			FindingOutcomeConcern,
			FindingSeverityCritical,
			authorityRuleContext{},
			nil,
		))
	case authorityNetworkContainerOpen:
		findings = append(findings, makeAuthorityFinding(
			"authority.network.container-open-egress",
			FindingOutcomeConcern,
			FindingSeverityHigh,
			authorityRuleContext{},
			nil,
		))
	case authorityNetworkContainerAllowlisted:
		if !facts.EgressDestinationsValid {
			findings = append(findings, makeAuthorityFinding(
				"authority.network.unknown",
				FindingOutcomeUnknown,
				FindingSeverityCritical,
				authorityRuleContext{},
				nil,
			))
		} else {
			findings = append(findings, makeAuthorityFinding(
				"authority.network.container-allowlist",
				FindingOutcomeBounded,
				FindingSeverityInfo,
				authorityRuleContext{Destinations: facts.EgressDestinations},
				nil,
			))
		}
	default:
		findings = append(findings, makeAuthorityFinding(
			"authority.network.unknown",
			FindingOutcomeUnknown,
			FindingSeverityCritical,
			authorityRuleContext{},
			nil,
		))
	}

	if facts.compatibilityRuleApplies("egress-ignored") {
		findings = append(findings, makeAuthorityFinding(
			"egress-ignored",
			FindingOutcomeConcern,
			FindingSeverityMedium,
			authorityRuleContext{Count: facts.EgressCount},
			nil,
		))
	}
	return findings
}

func evaluateFileAuthority(facts normalizedAuthorityFacts) []Finding {
	switch facts.Files {
	case authorityFilesHostAccount:
		return []Finding{makeAuthorityFinding(
			"authority.files.host-account",
			FindingOutcomeConcern,
			FindingSeverityCritical,
			authorityRuleContext{},
			nil,
		)}
	case authorityFilesWorkspace:
		return []Finding{makeAuthorityFinding(
			"authority.files.workspace",
			FindingOutcomeBounded,
			FindingSeverityInfo,
			authorityRuleContext{},
			nil,
		)}
	default:
		return []Finding{makeAuthorityFinding(
			"authority.files.unknown",
			FindingOutcomeUnknown,
			FindingSeverityCritical,
			authorityRuleContext{},
			nil,
		)}
	}
}

func evaluateProjectionAuthority(facts normalizedAuthorityFacts) []Finding {
	switch facts.Projection {
	case authorityProjectionNotApplicable:
		return []Finding{makeAuthorityFinding(
			"authority.projection.not-applicable",
			FindingOutcomeNotApplicable,
			FindingSeverityInfo,
			authorityRuleContext{},
			nil,
		)}
	case authorityProjectionAbsent:
		return []Finding{makeAuthorityFinding(
			"authority.projection.absent",
			FindingOutcomeBounded,
			FindingSeverityInfo,
			authorityRuleContext{},
			nil,
		)}
	case authorityProjectionLiveHostConfig:
		return []Finding{makeAuthorityFinding(
			"authority.projection.live-host-config",
			FindingOutcomeConcern,
			FindingSeverityMedium,
			authorityRuleContext{Count: len(facts.ProjectionLabels)},
			nil,
		)}
	default:
		return []Finding{makeAuthorityFinding(
			"authority.projection.unknown",
			FindingOutcomeUnknown,
			FindingSeverityHigh,
			authorityRuleContext{},
			nil,
		)}
	}
}

func evaluateSecretAuthority(facts normalizedAuthorityFacts) []Finding {
	if len(facts.SecretNames) == 0 {
		return []Finding{makeAuthorityFinding(
			"authority.secrets.absent",
			FindingOutcomeBounded,
			FindingSeverityInfo,
			authorityRuleContext{},
			nil,
		)}
	}
	return []Finding{makeAuthorityFinding(
		"authority.secrets.injected",
		FindingOutcomeConcern,
		FindingSeverityHigh,
		authorityRuleContext{Count: len(facts.SecretNames)},
		nil,
	)}
}

func makeAuthorityFinding(ruleID, outcome, severity string, context authorityRuleContext, scopeIDs []string) Finding {
	rule, ok := authorityRulesByID[ruleID]
	if !ok {
		// Emission sites use constants registered above. Keep a loud fallback if a
		// future programmer violates that invariant; validation still rejects it.
		return Finding{
			RuleID:      ruleID,
			Axis:        FindingAxisCredentials,
			Outcome:     FindingOutcomeUnknown,
			Severity:    FindingSeverityCritical,
			Title:       "Unregistered authority rule",
			Consequence: "An unregistered engine rule prevented a complete authority classification.",
			ScopeIDs:    nonNilStrings(scopeIDs),
			Remediation: &Remediation{
				Kind: RemediationKindRetryCheck, ActionID: "update-evaluation-engine",
				Summary: "Update safeslop and retry the evaluation.", DocsRef: evaluationDocs,
			},
		}
	}
	return Finding{
		RuleID:      rule.ID,
		Axis:        rule.Axis,
		Outcome:     outcome,
		Severity:    severity,
		Title:       rule.Title,
		Consequence: rule.Consequence(context),
		ScopeIDs:    nonNilStrings(scopeIDs),
		Remediation: cloneRemediation(rule.Remediation),
	}
}

func cloneRemediation(in *Remediation) *Remediation {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func nonNilStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

type credentialScopeDraft struct {
	CredentialScope
	Unknown         bool
	WriteCredential bool
}

type credentialProviderAuthority struct {
	Provider      string
	Drafts        []credentialScopeDraft
	ScopeIDs      []string
	WriteScopeIDs []string
	Unknown       bool
	HighAuthority bool
	Persistent    bool
}

func normalizeCredentialAuthority(c *Credentials) ([]CredentialScope, []credentialProviderAuthority) {
	if c == nil {
		return []CredentialScope{}, nil
	}
	providers := make([]credentialProviderAuthority, 0, 6)
	if len(c.Pnpm) > 0 {
		providers = append(providers, pnpmCredentialAuthority(c.Pnpm))
	}
	if c.Aws != nil {
		providers = append(providers, awsCredentialAuthority(c.Aws))
	}
	if c.Gcp != nil {
		providers = append(providers, gcpCredentialAuthority(c.Gcp))
	}
	if c.Kube != nil {
		providers = append(providers, kubeCredentialAuthority(c.Kube))
	}
	if c.Github != nil {
		providers = append(providers, githubCredentialAuthority(c.Github))
	}
	if c.Forgejo != nil {
		providers = append(providers, forgejoCredentialAuthority(c.Forgejo))
	}

	scopes := make([]CredentialScope, 0)
	for i := range providers {
		provider := &providers[i]
		sort.SliceStable(provider.Drafts, func(i, j int) bool {
			a, b := provider.Drafts[i].CredentialScope, provider.Drafts[j].CredentialScope
			if a.Target != b.Target {
				return a.Target < b.Target
			}
			if a.Access != b.Access {
				return a.Access < b.Access
			}
			if a.Lifetime != b.Lifetime {
				return a.Lifetime < b.Lifetime
			}
			return a.Basis < b.Basis
		})
		provider.ScopeIDs = make([]string, 0, len(provider.Drafts))
		provider.WriteScopeIDs = make([]string, 0, len(provider.Drafts))
		for n, draft := range provider.Drafts {
			draft.ScopeID = fmt.Sprintf("credential.%s.%03d", provider.Provider, n+1)
			draft.Provider = provider.Provider
			scopes = append(scopes, draft.CredentialScope)
			provider.ScopeIDs = append(provider.ScopeIDs, draft.ScopeID)
			if draft.WriteCredential {
				provider.WriteScopeIDs = append(provider.WriteScopeIDs, draft.ScopeID)
			}
			provider.Unknown = provider.Unknown || draft.Unknown
		}
	}
	return scopes, providers
}

func pnpmCredentialAuthority(registries []PnpmRegistry) credentialProviderAuthority {
	provider := credentialProviderAuthority{
		Provider: CredentialProviderPnpm, HighAuthority: true, Persistent: true,
		Drafts: make([]credentialScopeDraft, 0, len(registries)),
	}
	for _, registry := range registries {
		target := registry.Host
		unknown := registry.Token == "" || !safeRegistryHost(target)
		if !safeRegistryHost(target) {
			target = "declared registry"
		}
		provider.Drafts = append(provider.Drafts, credentialScopeDraft{
			CredentialScope: CredentialScope{
				Target: target, Access: CredentialAccessExternalPolicy,
				Lifetime: CredentialLifetimePersistent, Basis: CredentialBasisDeclared,
			},
			Unknown: unknown,
		})
	}
	return provider
}

func awsCredentialAuthority(aws *AwsSso) credentialProviderAuthority {
	target := "declared AWS identity"
	basis := CredentialBasisResolvedAtLaunch
	unknown := false
	switch {
	case aws.RoleArn != "" && safeValueFreeMetadata(aws.RoleArn):
		target = "role " + aws.RoleArn
	case aws.RoleArn != "":
		unknown = true
	case aws.Profile != "" && safeValueFreeMetadata(aws.Profile):
		target = "profile " + aws.Profile
	default:
		unknown = true
	}
	if (aws.RoleArn == "") != (aws.SessionPolicy == "") {
		unknown = true
	}
	return credentialProviderAuthority{
		Provider: CredentialProviderAWS, HighAuthority: true,
		Drafts: []credentialScopeDraft{{
			CredentialScope: CredentialScope{
				Target: target, Access: CredentialAccessExternalPolicy,
				Lifetime: CredentialLifetimeShortLived, Basis: basis,
			},
			Unknown: unknown,
		}},
	}
}

func gcpCredentialAuthority(gcp *GcpAdc) credentialProviderAuthority {
	provider := credentialProviderAuthority{Provider: CredentialProviderGCP, HighAuthority: true}
	if len(gcp.Scopes) == 0 {
		provider.Drafts = []credentialScopeDraft{{CredentialScope: CredentialScope{
			Target: "ADC default scopes", Access: CredentialAccessProviderDefault,
			Lifetime: CredentialLifetimeShortLived, Basis: CredentialBasisProviderDefault,
		}}}
		return provider
	}
	provider.Drafts = make([]credentialScopeDraft, 0, len(gcp.Scopes))
	for _, scope := range gcp.Scopes {
		target := scope
		unknown := !safeValueFreeMetadata(target)
		if unknown {
			target = "declared GCP API scope"
		}
		provider.Drafts = append(provider.Drafts, credentialScopeDraft{
			CredentialScope: CredentialScope{
				Target: target, Access: CredentialAccessScopedAPI,
				Lifetime: CredentialLifetimeShortLived, Basis: CredentialBasisDeclared,
			},
			Unknown: unknown,
		})
	}
	return provider
}

func kubeCredentialAuthority(kube *KubeCluster) credentialProviderAuthority {
	target := "declared cluster"
	unknown := false
	switch {
	case kube.Eks != nil && kube.Gke == nil:
		if safeValueFreeMetadata(kube.Eks.Name) {
			target = kube.Eks.Name
		} else {
			unknown = true
		}
	case kube.Gke != nil && kube.Eks == nil:
		if safeValueFreeMetadata(kube.Gke.Name) {
			target = kube.Gke.Name
		} else {
			unknown = true
		}
	default:
		unknown = true
	}
	return credentialProviderAuthority{
		Provider: CredentialProviderKube, HighAuthority: true,
		Drafts: []credentialScopeDraft{{
			CredentialScope: CredentialScope{
				Target: target, Access: CredentialAccessExternalPolicy,
				Lifetime: CredentialLifetimeShortLived, Basis: CredentialBasisDeclared,
			},
			Unknown: unknown,
		}},
	}
}

func githubCredentialAuthority(github *GithubCreds) credentialProviderAuthority {
	mode := github.Mode
	if mode == "" {
		mode = "app"
	}
	lifetime := CredentialLifetimeShortLived
	unknownMode := false
	switch mode {
	case "app":
		if github.Pat != "" {
			unknownMode = true
		}
	case "pat":
		lifetime = CredentialLifetimePersistent
		if github.Pat == "" || len(github.Repos) == 0 {
			unknownMode = true
		}
	default:
		lifetime = CredentialLifetimeUnknown
		unknownMode = true
	}
	provider := credentialProviderAuthority{
		Provider:   CredentialProviderGitHub,
		Persistent: lifetime == CredentialLifetimePersistent,
	}
	provider.Drafts = repositoryCredentialDrafts(
		github.Repos,
		github.Write,
		lifetime,
		"declared GitHub repository",
		unknownMode,
	)
	if len(github.Repos) == 0 {
		provider.Drafts = []credentialScopeDraft{{
			CredentialScope: CredentialScope{
				Target: "origin", Access: credentialWriteAccess(github.Write),
				Lifetime: lifetime, Basis: CredentialBasisResolvedAtLaunch,
			},
			Unknown: unknownMode, WriteCredential: github.Write,
		}}
	}
	if github.Api != nil && github.Api.Enabled {
		apiDrafts, unknown := githubAPIDrafts(github.Api, lifetime)
		provider.Drafts = append(provider.Drafts, apiDrafts...)
		provider.Unknown = provider.Unknown || unknown
		provider.HighAuthority = true
	}
	for _, draft := range provider.Drafts {
		provider.HighAuthority = provider.HighAuthority || draft.WriteCredential || draft.Unknown
	}
	return provider
}

func githubAPIDrafts(api *GithubApi, lifetime string) ([]credentialScopeDraft, bool) {
	if len(api.Permissions) == 0 {
		return []credentialScopeDraft{{CredentialScope: CredentialScope{
			Target: "GitHub API default permissions", Access: CredentialAccessProviderDefault,
			Lifetime: lifetime, Basis: CredentialBasisProviderDefault,
		}}}, false
	}
	drafts := make([]credentialScopeDraft, 0, len(api.Permissions))
	unknown := false
	for _, permission := range api.Permissions {
		target := permission
		bad := !safeValueFreeMetadata(target)
		if bad {
			target = "declared GitHub API permission"
			unknown = true
		} else {
			target += " — repository and permission downscoped"
		}
		drafts = append(drafts, credentialScopeDraft{
			CredentialScope: CredentialScope{
				Target: target, Access: CredentialAccessScopedAPI,
				Lifetime: lifetime, Basis: CredentialBasisDeclared,
			},
			Unknown: bad,
		})
	}
	return drafts, unknown
}

func forgejoCredentialAuthority(forgejo *ForgejoCreds) credentialProviderAuthority {
	provider := credentialProviderAuthority{Provider: CredentialProviderForgejo}
	provider.Drafts = repositoryCredentialDrafts(
		forgejo.Repos,
		forgejo.Write,
		CredentialLifetimeShortLived,
		"declared Forgejo repository",
		false,
	)
	if len(forgejo.Repos) == 0 {
		provider.Drafts = []credentialScopeDraft{{
			CredentialScope: CredentialScope{
				Target: "origin", Access: credentialWriteAccess(forgejo.Write),
				Lifetime: CredentialLifetimeShortLived, Basis: CredentialBasisResolvedAtLaunch,
			},
			WriteCredential: forgejo.Write,
		}}
	}
	if len(forgejo.Repos) > 1 && !safeForgeURL(forgejo.URL) {
		provider.Unknown = true
	}
	if forgejo.Api != nil && forgejo.Api.Enabled {
		provider.Drafts = append(provider.Drafts, credentialScopeDraft{CredentialScope: CredentialScope{
			Target: "operator-provisioned scope unverified; may be account-wide", Access: CredentialAccessExternalPolicy,
			Lifetime: CredentialLifetimePersistent, Basis: CredentialBasisProviderDefault,
		}, Unknown: !forgejo.Api.AckAccountWide})
		provider.Persistent = true
		provider.HighAuthority = true
	}
	for _, draft := range provider.Drafts {
		provider.HighAuthority = provider.HighAuthority || draft.WriteCredential || draft.Unknown
	}
	return provider
}

func repositoryCredentialDrafts(repos []RepoCred, providerWrite bool, lifetime, fallback string, forceUnknown bool) []credentialScopeDraft {
	drafts := make([]credentialScopeDraft, 0, len(repos))
	for _, repo := range repos {
		effectiveWrite := providerWrite || repo.Write
		target := repo.Repo
		unknown := forceUnknown || !safeRepositoryTarget(target)
		if !safeRepositoryTarget(target) {
			target = fallback
		}
		access := credentialWriteAccess(effectiveWrite)
		if forceUnknown {
			// A future credential mode may grant more than repository read, so its
			// scope must not inherit the current mode's read-only default.
			access = CredentialAccessUnknown
		}
		if lifetime == CredentialLifetimeUnknown {
			unknown = true
		}
		drafts = append(drafts, credentialScopeDraft{
			CredentialScope: CredentialScope{
				Target: target, Access: access, Lifetime: lifetime, Basis: CredentialBasisDeclared,
			},
			Unknown: unknown, WriteCredential: effectiveWrite,
		})
	}
	return drafts
}

func credentialWriteAccess(write bool) string {
	if write {
		return CredentialAccessReadWrite
	}
	return CredentialAccessReadOnly
}

var (
	stableIDPattern     = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	actionIDPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	credentialIDPattern = regexp.MustCompile(`^credential\.(pnpm|aws|gcp|kube|github|forgejo)\.[0-9]{3}$`)
	repositoryPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	registryHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)
)

func valueFreeEgressDestinations(in []string) ([]string, bool) {
	if len(in) == 0 {
		return []string{}, true
	}
	out := make([]string, 0, len(in))
	for _, destination := range in {
		candidate := strings.TrimPrefix(destination, ".")
		if candidate == "" || strings.Contains(candidate, "..") || !registryHostPattern.MatchString(candidate) || !safeValueFreeMetadata(destination) {
			return []string{}, false
		}
		out = append(out, destination)
	}
	sort.Strings(out)
	return out, true
}

func safeRegistryHost(host string) bool {
	return host != "" && !strings.Contains(host, "..") && registryHostPattern.MatchString(host) && safeValueFreeMetadata(host)
}

func safeRepositoryTarget(repo string) bool {
	return repositoryPattern.MatchString(repo) && safeMetadataBasics(repo)
}

func safeForgeURL(raw string) bool {
	if raw == "" || !safeValueFreeMetadata(raw) {
		return false
	}
	return strings.HasPrefix(raw, "https://") && !strings.Contains(strings.TrimPrefix(raw, "https://"), "@")
}

func safeValueFreeMetadata(value string) bool {
	if !safeMetadataBasics(value) {
		return false
	}
	if strings.Contains(value, "/") {
		lower := strings.ToLower(value)
		isHTTPSLabel := strings.HasPrefix(lower, "https://") && !strings.Contains(strings.TrimPrefix(lower, "https://"), "@")
		isARNLabel := strings.HasPrefix(value, "arn:") || strings.HasPrefix(value, "role arn:")
		if !isHTTPSLabel && !isARNLabel {
			// Slash-bearing free-form labels are indistinguishable from private
			// relative paths. Repository targets use their stricter own shape.
			return false
		}
	}
	return true
}

func safeMetadataBasics(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 512 || containsForbiddenMaterial(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			return false
		}
	}
	return true
}

func containsForbiddenMaterial(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"op://", "env:", "~/", "$home/", "${home}/", "../", "..\\", ".ssh/", "secrets.env",
		"private_key", "private-key", "private key ref", "key-ref", "token-ref", "account-link:",
		"/users/", "/home/", "/private/", "/tmp/", "c:\\users\\",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "/")
}

// ValidateFindingRegistry verifies stable IDs and ordinals, closed enum
// combinations, owned consequence/remediation policy, and value-free text.
func ValidateFindingRegistry() error {
	return validateAuthorityRuleRegistry(authorityRuleRegistry)
}

func validateAuthorityRuleRegistry(rules []authorityRule) error {
	ids := make(map[string]struct{}, len(rules))
	ordinals := make(map[string]map[int]string)
	for _, rule := range rules {
		if !stableIDPattern.MatchString(rule.ID) {
			return fmt.Errorf("rule ID %q is not a lowercase stable symbol", rule.ID)
		}
		if _, exists := ids[rule.ID]; exists {
			return fmt.Errorf("duplicate rule ID %q", rule.ID)
		}
		ids[rule.ID] = struct{}{}
		if !isAuthorityAxis(rule.Axis) {
			return fmt.Errorf("rule %q has invalid authority axis %q", rule.ID, rule.Axis)
		}
		if rule.Ordinal <= 0 {
			return fmt.Errorf("rule %q has invalid ordinal %d", rule.ID, rule.Ordinal)
		}
		if ordinals[rule.Axis] == nil {
			ordinals[rule.Axis] = map[int]string{}
		}
		if prior := ordinals[rule.Axis][rule.Ordinal]; prior != "" {
			return fmt.Errorf("duplicate %s ordinal %d for %q and %q", rule.Axis, rule.Ordinal, prior, rule.ID)
		}
		ordinals[rule.Axis][rule.Ordinal] = rule.ID
		if rule.Title == "" || containsForbiddenMaterial(rule.Title) {
			return fmt.Errorf("rule %q has missing or forbidden title", rule.ID)
		}
		if rule.Consequence == nil {
			return fmt.Errorf("rule %q has no consequence builder", rule.ID)
		}
		if consequence := rule.Consequence(authorityRuleContext{}); consequence == "" || containsForbiddenMaterial(consequence) {
			return fmt.Errorf("rule %q consequence builder emitted missing or forbidden material", rule.ID)
		}
		if len(rule.Allowed) == 0 {
			return fmt.Errorf("rule %q has no allowed enum combinations", rule.ID)
		}
		allowed := map[findingDisposition]struct{}{}
		requiresRemediation := false
		for _, disposition := range rule.Allowed {
			if !isFindingOutcome(disposition.Outcome) || !isFindingSeverity(disposition.Severity) {
				return fmt.Errorf("rule %q has invalid enum combination %s/%s", rule.ID, disposition.Outcome, disposition.Severity)
			}
			if _, duplicate := allowed[disposition]; duplicate {
				return fmt.Errorf("rule %q duplicates enum combination %s/%s", rule.ID, disposition.Outcome, disposition.Severity)
			}
			allowed[disposition] = struct{}{}
			requiresRemediation = requiresRemediation || outcomeRequiresRemediation(disposition.Outcome)
		}
		if requiresRemediation && rule.Remediation == nil {
			return fmt.Errorf("rule %q requires remediation", rule.ID)
		}
		if !requiresRemediation && rule.Remediation != nil {
			return fmt.Errorf("rule %q attaches remediation to a bounded/pass/not-applicable disposition", rule.ID)
		}
		if rule.Remediation != nil {
			if err := validateRemediation(*rule.Remediation); err != nil {
				return fmt.Errorf("rule %q remediation: %w", rule.ID, err)
			}
		}
	}
	return nil
}

// ValidateAuthorityEvaluation checks emitted findings against the registry and
// rejects value-bearing material, missing core axes, invalid scopes, dangling
// scope IDs, and nondeterministic ordering.
func ValidateAuthorityEvaluation(authority AuthorityEvaluation) error {
	if authority.Findings == nil {
		return fmt.Errorf("authority findings must be an array, not null")
	}
	if authority.CredentialScopes == nil {
		return fmt.Errorf("credential scopes must be an array, not null")
	}

	scopes := make(map[string]CredentialScope, len(authority.CredentialScopes))
	referenced := make(map[string]bool, len(authority.CredentialScopes))
	for i, scope := range authority.CredentialScopes {
		if err := validateCredentialScope(scope); err != nil {
			return fmt.Errorf("credential scope %d: %w", i, err)
		}
		if _, duplicate := scopes[scope.ScopeID]; duplicate {
			return fmt.Errorf("duplicate credential scope ID %q", scope.ScopeID)
		}
		scopes[scope.ScopeID] = scope
		if i > 0 && credentialScopeLess(scope, authority.CredentialScopes[i-1]) {
			return fmt.Errorf("credential scopes are not in deterministic provider/target order")
		}
	}

	seenRules := make(map[string]bool, len(authority.Findings))
	seenAxes := make(map[string]bool, 5)
	for i, finding := range authority.Findings {
		rule, ok := authorityRulesByID[finding.RuleID]
		if !ok {
			return fmt.Errorf("unregistered finding ID %q", finding.RuleID)
		}
		if seenRules[finding.RuleID] {
			return fmt.Errorf("duplicate finding ID %q", finding.RuleID)
		}
		seenRules[finding.RuleID] = true
		seenAxes[finding.Axis] = true
		if finding.Axis != rule.Axis || finding.Title != rule.Title {
			return fmt.Errorf("finding %q does not match registry axis/title", finding.RuleID)
		}
		if !ruleAllows(rule, finding.Outcome, finding.Severity) {
			return fmt.Errorf("finding %q has invalid enum combination %s/%s", finding.RuleID, finding.Outcome, finding.Severity)
		}
		if finding.Consequence == "" || containsForbiddenMaterial(finding.Consequence) {
			return fmt.Errorf("finding %q has missing or forbidden consequence material", finding.RuleID)
		}
		if finding.ScopeIDs == nil {
			return fmt.Errorf("finding %q scope_ids must be an array, not null", finding.RuleID)
		}
		if !sort.StringsAreSorted(finding.ScopeIDs) {
			return fmt.Errorf("finding %q scope IDs are not deterministic", finding.RuleID)
		}
		for _, scopeID := range finding.ScopeIDs {
			if _, ok := scopes[scopeID]; !ok {
				return fmt.Errorf("finding %q references unknown scope ID %q", finding.RuleID, scopeID)
			}
			referenced[scopeID] = true
		}
		if outcomeRequiresRemediation(finding.Outcome) {
			if finding.Remediation == nil {
				return fmt.Errorf("finding %q requires remediation", finding.RuleID)
			}
		} else if finding.Remediation != nil {
			return fmt.Errorf("finding %q must not attach remediation to %q", finding.RuleID, finding.Outcome)
		}
		if !remediationEqual(finding.Remediation, rule.Remediation) {
			return fmt.Errorf("finding %q remediation does not match registry policy", finding.RuleID)
		}
		if i > 0 && authorityFindingLess(finding, authority.Findings[i-1]) {
			return fmt.Errorf("authority findings are not in deterministic axis/rule order")
		}
	}
	for _, axis := range []string{
		FindingAxisNetwork,
		FindingAxisFiles,
		FindingAxisProjection,
		FindingAxisSecrets,
		FindingAxisCredentials,
	} {
		if !seenAxes[axis] {
			return fmt.Errorf("core authority axis %q is absent", axis)
		}
	}
	for scopeID := range scopes {
		if !referenced[scopeID] {
			return fmt.Errorf("credential scope %q is not referenced by any finding", scopeID)
		}
	}
	return nil
}

func validateCredentialScope(scope CredentialScope) error {
	if !credentialIDPattern.MatchString(scope.ScopeID) || !strings.HasPrefix(scope.ScopeID, "credential."+scope.Provider+".") {
		return fmt.Errorf("scope ID %q is not an engine-only provider symbol", scope.ScopeID)
	}
	if !isCredentialProvider(scope.Provider) {
		return fmt.Errorf("unknown provider %q", scope.Provider)
	}
	targetSafe := safeValueFreeMetadata(scope.Target)
	if scope.Provider == CredentialProviderGitHub || scope.Provider == CredentialProviderForgejo {
		targetSafe = targetSafe || safeRepositoryTarget(scope.Target)
	}
	if !targetSafe {
		return fmt.Errorf("target contains missing or forbidden material")
	}
	if !isCredentialAccess(scope.Access) {
		return fmt.Errorf("unknown access %q", scope.Access)
	}
	if !isCredentialLifetime(scope.Lifetime) {
		return fmt.Errorf("unknown lifetime %q", scope.Lifetime)
	}
	if !isCredentialBasis(scope.Basis) {
		return fmt.Errorf("unknown basis %q", scope.Basis)
	}
	return nil
}

func validateRemediation(remediation Remediation) error {
	if !isRemediationKind(remediation.Kind) {
		return fmt.Errorf("unknown kind %q", remediation.Kind)
	}
	if !actionIDPattern.MatchString(remediation.ActionID) {
		return fmt.Errorf("invalid action ID %q", remediation.ActionID)
	}
	if remediation.Summary == "" || containsForbiddenMaterial(remediation.Summary) {
		return fmt.Errorf("missing or forbidden summary")
	}
	if remediation.DocsRef == "" || containsForbiddenMaterial(remediation.DocsRef) {
		return fmt.Errorf("missing or forbidden docs_ref")
	}
	return nil
}

func remediationEqual(a, b *Remediation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Kind == b.Kind && a.ActionID == b.ActionID && a.Summary == b.Summary && a.DocsRef == b.DocsRef
}

func ruleAllows(rule authorityRule, outcome, severity string) bool {
	for _, allowed := range rule.Allowed {
		if allowed.Outcome == outcome && allowed.Severity == severity {
			return true
		}
	}
	return false
}

func authorityFindingLess(a, b Finding) bool {
	if axisOrder(a.Axis) != axisOrder(b.Axis) {
		return axisOrder(a.Axis) < axisOrder(b.Axis)
	}
	if outcomeOrder(a.Outcome) != outcomeOrder(b.Outcome) {
		return outcomeOrder(a.Outcome) < outcomeOrder(b.Outcome)
	}
	if severityOrder(a.Severity) != severityOrder(b.Severity) {
		return severityOrder(a.Severity) < severityOrder(b.Severity)
	}
	return authorityRulesByID[a.RuleID].Ordinal < authorityRulesByID[b.RuleID].Ordinal
}

func credentialScopeLess(a, b CredentialScope) bool {
	if credentialProviderOrder(a.Provider) != credentialProviderOrder(b.Provider) {
		return credentialProviderOrder(a.Provider) < credentialProviderOrder(b.Provider)
	}
	if a.Target != b.Target {
		return a.Target < b.Target
	}
	if a.Access != b.Access {
		return a.Access < b.Access
	}
	if a.Lifetime != b.Lifetime {
		return a.Lifetime < b.Lifetime
	}
	if a.Basis != b.Basis {
		return a.Basis < b.Basis
	}
	return a.ScopeID < b.ScopeID
}

func axisOrder(axis string) int {
	switch axis {
	case FindingAxisNetwork:
		return 0
	case FindingAxisFiles:
		return 1
	case FindingAxisProjection:
		return 2
	case FindingAxisSecrets:
		return 3
	case FindingAxisCredentials:
		return 4
	default:
		return 99
	}
}

func outcomeOrder(outcome string) int {
	switch outcome {
	case FindingOutcomeConcern, FindingOutcomeFail, FindingOutcomeUnknown:
		return 0
	case FindingOutcomeBounded:
		return 1
	case FindingOutcomePass:
		return 2
	case FindingOutcomeNotApplicable:
		return 3
	default:
		return 99
	}
}

func severityOrder(severity string) int {
	switch severity {
	case FindingSeverityCritical:
		return 0
	case FindingSeverityHigh:
		return 1
	case FindingSeverityMedium:
		return 2
	case FindingSeverityInfo:
		return 3
	default:
		return 99
	}
}

func credentialProviderOrder(provider string) int {
	switch provider {
	case CredentialProviderPnpm:
		return 0
	case CredentialProviderAWS:
		return 1
	case CredentialProviderGCP:
		return 2
	case CredentialProviderKube:
		return 3
	case CredentialProviderGitHub:
		return 4
	case CredentialProviderForgejo:
		return 5
	default:
		return 99
	}
}

func outcomeRequiresRemediation(outcome string) bool {
	return outcome == FindingOutcomeConcern || outcome == FindingOutcomeFail || outcome == FindingOutcomeUnknown
}

func isAuthorityAxis(axis string) bool {
	return axisOrder(axis) < 99
}

func isFindingOutcome(outcome string) bool {
	switch outcome {
	case FindingOutcomeConcern, FindingOutcomeBounded, FindingOutcomePass, FindingOutcomeFail, FindingOutcomeUnknown, FindingOutcomeNotApplicable:
		return true
	default:
		return false
	}
}

func isFindingSeverity(severity string) bool {
	switch severity {
	case FindingSeverityCritical, FindingSeverityHigh, FindingSeverityMedium, FindingSeverityInfo:
		return true
	default:
		return false
	}
}

func isRemediationKind(kind string) bool {
	switch kind {
	case RemediationKindPolicyChange,
		RemediationKindOperatorWorkflow,
		RemediationKindInstallHelper,
		RemediationKindRepairHelperResolution,
		RemediationKindReviewAndTrust,
		RemediationKindRetryCheck:
		return true
	default:
		return false
	}
}

func isCredentialProvider(provider string) bool {
	return credentialProviderOrder(provider) < 99
}

func isCredentialAccess(access string) bool {
	switch access {
	case CredentialAccessReadOnly,
		CredentialAccessReadWrite,
		CredentialAccessScopedAPI,
		CredentialAccessExternalPolicy,
		CredentialAccessProviderDefault,
		CredentialAccessUnknown:
		return true
	default:
		return false
	}
}

func isCredentialLifetime(lifetime string) bool {
	switch lifetime {
	case CredentialLifetimeShortLived, CredentialLifetimePersistent, CredentialLifetimeUnknown:
		return true
	default:
		return false
	}
}

func isCredentialBasis(basis string) bool {
	switch basis {
	case CredentialBasisDeclared, CredentialBasisResolvedAtLaunch, CredentialBasisProviderDefault:
		return true
	default:
		return false
	}
}
