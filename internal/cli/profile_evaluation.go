package cli

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	runtimepkg "github.com/freakhill/safeslop/internal/engine/container/runtime"
	"github.com/freakhill/safeslop/internal/engine/hostexec"
	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/engine/toolchain"
	"github.com/freakhill/safeslop/internal/engine/trust"
	"github.com/freakhill/safeslop/internal/engine/userconfig"
	workspaceboundary "github.com/freakhill/safeslop/internal/engine/workspace"
)

type profileEvaluationSource string

const (
	profileEvaluationSourceProject profileEvaluationSource = "project"
	profileEvaluationSourceBuiltin profileEvaluationSource = "builtin"
	profileEvaluationSourceUnsaved profileEvaluationSource = "unsaved"
)

type profileEvaluationInput struct {
	Source      profileEvaluationSource
	Name        string
	PolicyPath  string
	PolicyHash  string
	PolicyBytes []byte
	Profile     policy.Profile
}

type profilePrerequisiteState uint8

type profilePrerequisiteProblem uint8

const (
	profilePrerequisiteUnknown profilePrerequisiteState = iota
	profilePrerequisitePass
	profilePrerequisiteFail
)

const (
	profileProblemNone profilePrerequisiteProblem = iota
	profileProblemMissing
	profileProblemResolution
	profileProblemCheck
)

type profilePrerequisiteCheck struct {
	State   profilePrerequisiteState
	Problem profilePrerequisiteProblem
}

type profileProviderLinkCheck struct {
	Required   bool
	State      profilePrerequisiteState
	Count      int
	RequiresOp bool
}

type profileAccountLinkChecks struct {
	Github  profileProviderLinkCheck
	Forgejo profileProviderLinkCheck
}

// These adapters deliberately return classifications, never paths, account
// records, helper output, or credential refs. Each root owns the replaceable
// functions in its dependency bundle.
func defaultProfileEvaluationProjectTrust(path string, exactBytes []byte) (trust.Status, error) {
	_, status, err := policyBytesTrustStatus(canonicalPolicyPath(path), exactBytes)
	return status, err
}

func defaultProfileEvaluationBuiltinIntegrity(name, hash string) (bool, error) {
	builtin, ok := policy.BuiltinProfileByName(name)
	return ok && builtin.Hash == hash, nil
}

func defaultProfileEvaluationWorkspace(path string) profilePrerequisiteCheck {
	if path == "" {
		return profilePrerequisiteCheck{State: profilePrerequisiteUnknown, Problem: profileProblemCheck}
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemMissing}
		}
		return profilePrerequisiteCheck{State: profilePrerequisiteUnknown, Problem: profileProblemCheck}
	}
	if !info.IsDir() {
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemMissing}
	}
	if err := validateWorkspaceStageRoot(path); err != nil {
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemResolution}
	}
	return profilePrerequisiteCheck{State: profilePrerequisitePass}
}

func inspectProfileEvaluationHelperWithResolver(resolver *hostexec.Resolver, name string) profilePrerequisiteCheck {
	inspection := resolver.Inspect(name)
	switch {
	case inspection.Shadowed:
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemResolution}
	case errors.Is(inspection.Err, hostexec.ErrIdentity), errors.Is(inspection.Err, hostexec.ErrRelativePath):
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemResolution}
	case !inspection.Present || errors.Is(inspection.Err, hostexec.ErrNotFound):
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemMissing}
	case inspection.Err != nil:
		return profilePrerequisiteCheck{State: profilePrerequisiteUnknown, Problem: profileProblemCheck}
	default:
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
}

func inspectProfileEvaluationRuntime(detect func(runtimepkg.NetworkPolicy) (runtimepkg.Engine, error), network string) profilePrerequisiteCheck {
	networkPolicy := runtimepkg.PolicyAllow
	if network == "deny" {
		networkPolicy = runtimepkg.PolicyDeny
	}
	_, err := detect(networkPolicy)
	if err == nil {
		return profilePrerequisiteCheck{State: profilePrerequisitePass}
	}
	if errors.Is(err, hostexec.ErrShadowed) || errors.Is(err, hostexec.ErrIdentity) || errors.Is(err, hostexec.ErrRelativePath) {
		return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemResolution}
	}
	return profilePrerequisiteCheck{State: profilePrerequisiteFail, Problem: profileProblemMissing}
}

func inspectProfileEvaluationAccountLinks(prof policy.Profile) profileAccountLinkChecks {
	var checks profileAccountLinkChecks
	credentials := prof.Credentials
	if credentials == nil {
		return checks
	}
	if github := credentials.Github; github != nil && github.Mode != "pat" {
		checks.Github.Required = true
	}
	if credentials.Forgejo != nil {
		checks.Forgejo.Required = true
	}
	if !checks.Github.Required && !checks.Forgejo.Required {
		return checks
	}

	path, err := userconfig.DefaultAccountsPath()
	if err != nil {
		return unknownRequiredAccountChecks(checks)
	}
	accounts, err := userconfig.LoadAccounts(path)
	if err != nil {
		return unknownRequiredAccountChecks(checks)
	}

	if checks.Github.Required {
		checks.Github = inspectGithubAccountLinks(credentials.Github, accounts)
	}
	if checks.Forgejo.Required {
		checks.Forgejo = inspectForgejoAccountLinks(credentials.Forgejo, accounts)
	}
	return checks
}

func unknownRequiredAccountChecks(checks profileAccountLinkChecks) profileAccountLinkChecks {
	if checks.Github.Required {
		checks.Github.State = profilePrerequisiteUnknown
	}
	if checks.Forgejo.Required {
		checks.Forgejo.State = profilePrerequisiteUnknown
	}
	return checks
}

func inspectGithubAccountLinks(github *policy.GithubCreds, accounts *userconfig.Accounts) profileProviderLinkCheck {
	check := profileProviderLinkCheck{Required: true, State: profilePrerequisitePass}
	owners := ownersFromRepos(github.Repos)
	if len(github.Repos) == 0 || len(owners) == 0 {
		check.State = profilePrerequisiteUnknown
		return check
	}
	check.Count = len(owners)
	for _, owner := range owners {
		account := accounts.Lookup("github.com", owner)
		if account == nil || account.Github == nil {
			check.State = profilePrerequisiteFail
			continue
		}
		check.RequiresOp = check.RequiresOp || strings.HasPrefix(account.Github.PrivateKeyRef, "op://")
	}
	return check
}

func inspectForgejoAccountLinks(forgejo *policy.ForgejoCreds, accounts *userconfig.Accounts) profileProviderLinkCheck {
	check := profileProviderLinkCheck{Required: true, State: profilePrerequisitePass}
	host := forgejoPreflightHost(forgejo.URL)
	owners := ownersFromRepos(forgejo.Repos)
	if host == "" || len(forgejo.Repos) == 0 || len(owners) == 0 {
		check.State = profilePrerequisiteUnknown
		return check
	}
	check.Count = len(owners)
	for _, owner := range owners {
		account := accounts.Lookup(host, owner)
		if account == nil || account.Forgejo == nil {
			check.State = profilePrerequisiteFail
			continue
		}
		check.RequiresOp = check.RequiresOp || strings.HasPrefix(account.Forgejo.TokenRef, "op://")
	}
	return check
}

type profileFindingDisposition struct {
	Outcome  string
	Severity string
}

type profileContextRule struct {
	ID               string
	Axis             string
	Title            string
	Ordinal          int
	Allowed          []profileFindingDisposition
	RemediationKinds []string
}

var profileContextRuleRegistry = []profileContextRule{
	{ID: "trust.project.trusted", Axis: policy.FindingAxisTrust, Title: "Exact saved policy is trusted", Ordinal: 100, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}}},
	{ID: "trust.project.untrusted", Axis: policy.FindingAxisTrust, Title: "Saved policy has no approval", Ordinal: 110, Allowed: []profileFindingDisposition{{policy.FindingOutcomeFail, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindReviewAndTrust}},
	{ID: "trust.project.changed", Axis: policy.FindingAxisTrust, Title: "Saved policy changed after approval", Ordinal: 120, Allowed: []profileFindingDisposition{{policy.FindingOutcomeFail, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindReviewAndTrust}},
	{ID: "trust.project.unknown", Axis: policy.FindingAxisTrust, Title: "Saved policy trust could not be checked", Ordinal: 130, Allowed: []profileFindingDisposition{{policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindRetryCheck}},
	{ID: "trust.builtin.trusted", Axis: policy.FindingAxisTrust, Title: "Embedded builtin provenance is trusted", Ordinal: 140, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}}},
	{ID: "trust.builtin.unknown", Axis: policy.FindingAxisTrust, Title: "Embedded builtin provenance is inconsistent", Ordinal: 150, Allowed: []profileFindingDisposition{{policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindRetryCheck}},
	{ID: "trust.unsaved", Axis: policy.FindingAxisTrust, Title: "Unsaved policy has no trust state", Ordinal: 160, Allowed: []profileFindingDisposition{{policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}},
	{ID: "readiness.workspace", Axis: policy.FindingAxisReadiness, Title: "Workspace availability", Ordinal: 200, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindOperatorWorkflow, policy.RemediationKindRetryCheck}},
	{ID: "readiness.container-runtime", Axis: policy.FindingAxisReadiness, Title: "Container runtime readiness", Ordinal: 210, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}, {policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}, RemediationKinds: []string{policy.RemediationKindInstallHelper, policy.RemediationKindRepairHelperResolution, policy.RemediationKindRetryCheck}},
	{ID: "readiness.agent", Axis: policy.FindingAxisReadiness, Title: "Agent executable readiness", Ordinal: 220, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}, {policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}, RemediationKinds: []string{policy.RemediationKindInstallHelper, policy.RemediationKindRepairHelperResolution, policy.RemediationKindRetryCheck}},
	{ID: "readiness.secret-provider", Axis: policy.FindingAxisReadiness, Title: "Credential-staging helper readiness", Ordinal: 230, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}, {policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}, RemediationKinds: []string{policy.RemediationKindInstallHelper, policy.RemediationKindRepairHelperResolution, policy.RemediationKindRetryCheck}},
	{ID: "readiness.toolchain.mise", Axis: policy.FindingAxisReadiness, Title: "Mise toolchain readiness", Ordinal: 240, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindInstallHelper, policy.RemediationKindRepairHelperResolution, policy.RemediationKindRetryCheck}},
	{ID: "readiness.toolchain.nix", Axis: policy.FindingAxisReadiness, Title: "Nix toolchain readiness", Ordinal: 250, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindInstallHelper, policy.RemediationKindRepairHelperResolution, policy.RemediationKindRetryCheck}},
	{ID: "readiness.toolchain.not-applicable", Axis: policy.FindingAxisReadiness, Title: "Toolchain helper is not required", Ordinal: 260, Allowed: []profileFindingDisposition{{policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}},
	{ID: "readiness.github-account", Axis: policy.FindingAxisReadiness, Title: "GitHub account-link readiness", Ordinal: 270, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}, {policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}, RemediationKinds: []string{policy.RemediationKindOperatorWorkflow, policy.RemediationKindRetryCheck}},
	{ID: "readiness.forgejo-account", Axis: policy.FindingAxisReadiness, Title: "Forgejo account-link readiness", Ordinal: 280, Allowed: []profileFindingDisposition{{policy.FindingOutcomePass, policy.FindingSeverityInfo}, {policy.FindingOutcomeFail, policy.FindingSeverityHigh}, {policy.FindingOutcomeUnknown, policy.FindingSeverityHigh}}, RemediationKinds: []string{policy.RemediationKindOperatorWorkflow, policy.RemediationKindRetryCheck}},
	{ID: "readiness.not-collected", Axis: policy.FindingAxisReadiness, Title: "Local readiness was not collected", Ordinal: 290, Allowed: []profileFindingDisposition{{policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo}}},
}

var profileContextRulesByID = func() map[string]profileContextRule {
	out := make(map[string]profileContextRule, len(profileContextRuleRegistry))
	for _, rule := range profileContextRuleRegistry {
		out[rule.ID] = rule
	}
	return out
}()

func init() {
	if err := validateProfileContextRegistry(profileContextRuleRegistry); err != nil {
		panic("invalid profile context finding registry: " + err.Error())
	}
}

func validateProfileContextRegistry(rules []profileContextRule) error {
	stableID := regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
	ids := map[string]bool{}
	ordinals := map[int]bool{}
	for _, rule := range rules {
		if !stableID.MatchString(rule.ID) || ids[rule.ID] {
			return fmt.Errorf("invalid or duplicate rule ID %q", rule.ID)
		}
		if rule.Axis != policy.FindingAxisTrust && rule.Axis != policy.FindingAxisReadiness {
			return fmt.Errorf("rule %q has invalid context axis %q", rule.ID, rule.Axis)
		}
		if rule.Title == "" || rule.Ordinal <= 0 || ordinals[rule.Ordinal] || len(rule.Allowed) == 0 {
			return fmt.Errorf("rule %q has incomplete or duplicate registry metadata", rule.ID)
		}
		ids[rule.ID], ordinals[rule.Ordinal] = true, true
	}
	return nil
}

const profileEvaluationDocs = "specs/0101-profile-safety-evaluation.md"

func evaluateProfile(input profileEvaluationInput) policy.Evaluation {
	return evaluateProfileWithDeps(defaultDependencies(), input)
}

func evaluateProfileWithDeps(d *dependencies, input profileEvaluationInput) policy.Evaluation {
	checkedAt := d.now().UTC()
	authority := policy.EvaluateAuthority(input.Profile)
	return policy.Evaluation{
		SchemaVersion: policy.EvaluationSchemaVersion,
		Authority:     authority,
		Trust:         evaluateProfileTrustWithDeps(d, input, checkedAt),
		Readiness:     evaluateProfileReadinessWithDeps(d, input, authority, checkedAt),
	}
}

func evaluateProfileTrustWithDeps(d *dependencies, input profileEvaluationInput, checkedAt time.Time) policy.TrustEvaluation {
	section := policy.TrustEvaluation{CheckedAt: &checkedAt, Findings: []policy.Finding{}}
	switch input.Source {
	case profileEvaluationSourceProject:
		status, err := d.profileProjectTrust(input.PolicyPath, input.PolicyBytes)
		if err != nil || input.PolicyPath == "" || input.PolicyBytes == nil {
			section.State, section.Basis = policy.TrustStateUnknown, policy.TrustBasisUnknown
			section.Findings = append(section.Findings, profileContextFinding(
				"trust.project.unknown", policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
				"The exact saved policy approval could not be checked, so launch must not treat it as trusted.", nil,
				contextRemediation(policy.RemediationKindRetryCheck, "retry-trust-check", "Retry after the local trust store is available.", "#trust"),
			))
			return section
		}
		section.Basis = policy.TrustBasisProjectExactBytes
		switch status {
		case trust.Trusted:
			section.State = policy.TrustStateTrusted
			section.Findings = append(section.Findings, profileContextFinding(
				"trust.project.trusted", policy.FindingOutcomePass, policy.FindingSeverityInfo,
				"The exact policy bytes used for this inspection match the host approval record.", nil, nil,
			))
		case trust.Changed:
			section.State = policy.TrustStateChanged
			section.Findings = append(section.Findings, profileContextFinding(
				"trust.project.changed", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
				"The saved policy bytes no longer match the recorded approval; launch remains blocked until explicit review and trust.", nil,
				trustRemediation(),
			))
		default:
			section.State = policy.TrustStateUntrusted
			section.Findings = append(section.Findings, profileContextFinding(
				"trust.project.untrusted", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
				"No approval matches these exact saved policy bytes; launch remains blocked until explicit review and trust.", nil,
				trustRemediation(),
			))
		}
	case profileEvaluationSourceBuiltin:
		consistent, err := d.profileBuiltinIntegrity(input.Name, input.PolicyHash)
		section.Basis = policy.TrustBasisEmbeddedBuiltin
		if err != nil || !consistent {
			section.State = policy.TrustStateUnknown
			section.Findings = append(section.Findings, profileContextFinding(
				"trust.builtin.unknown", policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
				"The embedded builtin registry and requested provenance are inconsistent; launch must fail closed.", nil,
				contextRemediation(policy.RemediationKindRetryCheck, "update-and-retry-builtin", "Update safeslop and retry the builtin inspection.", "#trust"),
			))
		} else {
			section.State = policy.TrustStateTrusted
			section.Findings = append(section.Findings, profileContextFinding(
				"trust.builtin.trusted", policy.FindingOutcomePass, policy.FindingSeverityInfo,
				"This profile comes from the running binary's known embedded builtin registry.", nil, nil,
			))
		}
	default:
		section.State, section.Basis, section.CheckedAt = policy.TrustStateNotApplicable, policy.TrustBasisUnsaved, nil
		section.Findings = append(section.Findings, profileContextFinding(
			"trust.unsaved", policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo,
			"This preview is not saved policy bytes, so exact-byte approval does not apply.", nil, nil,
		))
	}
	return section
}

func trustRemediation() *policy.Remediation {
	return contextRemediation(
		policy.RemediationKindReviewAndTrust,
		"review-and-trust-policy",
		"Review the exact saved policy, then use the explicit trust workflow.",
		"#trust",
	)
}

func evaluateProfileReadinessWithDeps(d *dependencies, input profileEvaluationInput, authority policy.AuthorityEvaluation, checkedAt time.Time) policy.ReadinessEvaluation {
	findings := make([]policy.Finding, 0, 8)

	workspacePath, workspaceKnown := profileWorkspacePath(input)
	workspaceCheck := profilePrerequisiteCheck{State: profilePrerequisiteUnknown, Problem: profileProblemCheck}
	if workspaceKnown {
		workspaceCheck = d.profileWorkspace(workspacePath)
	}
	findings = append(findings, workspaceFinding(workspaceCheck))

	if input.Profile.Environment == "container" {
		findings = append(findings, runtimeFinding(d.profileRuntime(input.Profile.Network)))
		findings = append(findings, profileContextFinding(
			"readiness.agent", policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo,
			"The agent executable is supplied by the resolved container image rather than a host helper.", nil, nil,
		))
	} else {
		findings = append(findings, profileContextFinding(
			"readiness.container-runtime", policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo,
			"A host profile does not require a container runtime.", nil, nil,
		))
		findings = append(findings, agentFinding(profileAgentCheckWithDeps(d, input.Profile)))
	}

	accountChecks := d.profileAccountLinks(input.Profile)
	helperNames := profileRequiredHelperNames(input.Profile, accountChecks)
	findings = append(findings, helperFindingWithDeps(d, helperNames))
	findings = append(findings, toolchainFindingWithDeps(d, input.Profile))
	findings = append(findings, accountFindings(input.Profile, accountChecks, authority.CredentialScopes)...)

	sort.SliceStable(findings, func(i, j int) bool {
		return profileContextRulesByID[findings[i].RuleID].Ordinal < profileContextRulesByID[findings[j].RuleID].Ordinal
	})
	state := policy.ReadinessStateReady
	for _, finding := range findings {
		if finding.Outcome == policy.FindingOutcomeFail {
			state = policy.ReadinessStateBlocked
			break
		}
		if finding.Outcome == policy.FindingOutcomeUnknown {
			state = policy.ReadinessStateUnknown
		}
	}
	return policy.ReadinessEvaluation{State: state, CheckedAt: &checkedAt, Findings: findings}
}

func profileWorkspacePath(input profileEvaluationInput) (string, bool) {
	invocationDir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	policyPath := ""
	if input.Source != profileEvaluationSourceBuiltin {
		policyPath = input.PolicyPath
	}
	candidate, err := workspaceboundary.Candidate(input.Profile.Workspace, policyPath, invocationDir)
	if err != nil {
		return "", false
	}
	if resolved, err := workspaceboundary.Resolve(input.Profile.Workspace, policyPath, invocationDir); err == nil {
		return resolved, true
	}
	return candidate, true
}

func workspaceFinding(check profilePrerequisiteCheck) policy.Finding {
	switch check.State {
	case profilePrerequisitePass:
		return profileContextFinding(
			"readiness.workspace", policy.FindingOutcomePass, policy.FindingSeverityInfo,
			"The configured workspace is available as a directory for this launch snapshot.", nil, nil,
		)
	case profilePrerequisiteFail:
		return profileContextFinding(
			"readiness.workspace", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
			"The configured workspace is not available as a directory, so launch cannot establish its declared file boundary.", nil,
			contextRemediation(policy.RemediationKindOperatorWorkflow, "repair-workspace", "Create or select an available workspace, then retry.", "#readiness"),
		)
	default:
		return profileContextFinding(
			"readiness.workspace", policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
			"Workspace availability could not be checked; readiness must not be assumed.", nil,
			contextRemediation(policy.RemediationKindRetryCheck, "retry-workspace-check", "Retry after local workspace access is restored.", "#readiness"),
		)
	}
}

func runtimeFinding(check profilePrerequisiteCheck) policy.Finding {
	switch check.State {
	case profilePrerequisitePass:
		return profileContextFinding(
			"readiness.container-runtime", policy.FindingOutcomePass, policy.FindingSeverityInfo,
			"A working, unambiguous container runtime is available for the profile's network posture.", nil, nil,
		)
	case profilePrerequisiteUnknown:
		return profileContextFinding(
			"readiness.container-runtime", policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
			"Container runtime readiness could not be checked; launch readiness must not be assumed.", nil,
			contextRemediation(policy.RemediationKindRetryCheck, "retry-container-runtime-check", "Retry after local runtime inspection is available.", "#readiness"),
		)
	case profilePrerequisiteFail:
		if check.Problem == profileProblemResolution {
			return profileContextFinding(
				"readiness.container-runtime", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
				"Container runtime helper resolution is shadowed or its identity cannot be verified; launch remains blocked.", nil,
				contextRemediation(policy.RemediationKindRepairHelperResolution, "repair-container-runtime-resolution", "Remove distinct helper shadows or repair sanitized PATH resolution.", "#readiness"),
			)
		}
		return profileContextFinding(
			"readiness.container-runtime", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
			"No selected container runtime passed its local capability and network-posture checks; launch remains blocked.", nil,
			contextRemediation(policy.RemediationKindInstallHelper, "install-container-runtime", "Install or start one supported container runtime and retry.", "#readiness"),
		)
	default:
		panic("unhandled runtime prerequisite state")
	}
}

func profileAgentCheckWithDeps(d *dependencies, prof policy.Profile) profilePrerequisiteCheck {
	argv, err := agentArgv(prof)
	if err != nil || len(argv) == 0 {
		return profilePrerequisiteCheck{State: profilePrerequisiteUnknown, Problem: profileProblemCheck}
	}
	return d.profileHelper(argv[0])
}

func agentFinding(check profilePrerequisiteCheck) policy.Finding {
	switch check.State {
	case profilePrerequisitePass:
		return profileContextFinding(
			"readiness.agent", policy.FindingOutcomePass, policy.FindingSeverityInfo,
			"The required host agent executable has one verified sanitized-PATH resolution.", nil, nil,
		)
	case profilePrerequisiteUnknown:
		return profileContextFinding(
			"readiness.agent", policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
			"The required host agent executable could not be inspected; launch readiness must not be assumed.", nil,
			contextRemediation(policy.RemediationKindRetryCheck, "retry-agent-helper-check", "Retry after sanitized helper inspection is available.", "#readiness"),
		)
	case profilePrerequisiteFail:
		if check.Problem == profileProblemResolution {
			return profileContextFinding(
				"readiness.agent", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
				"The host agent executable is shadowed or its identity cannot be verified; launch remains blocked.", nil,
				contextRemediation(policy.RemediationKindRepairHelperResolution, "repair-agent-helper-resolution", "Remove distinct helper shadows or repair sanitized PATH resolution.", "#readiness"),
			)
		}
		return profileContextFinding(
			"readiness.agent", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
			"The required host agent executable is unavailable on sanitized PATH; launch remains blocked.", nil,
			contextRemediation(policy.RemediationKindInstallHelper, "install-agent-helper", "Install the selected agent executable or repair sanitized PATH.", "#readiness"),
		)
	default:
		panic("unhandled agent prerequisite state")
	}
}

func profileRequiredHelperNames(prof policy.Profile, accountChecks profileAccountLinkChecks) []string {
	names := map[string]bool{}
	for _, spec := range requiredProfileHostHelpers(prof, nil) {
		names[spec.Name] = true
	}
	if accountChecks.Github.RequiresOp || accountChecks.Forgejo.RequiresOp {
		names["op"] = true
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func helperFindingWithDeps(d *dependencies, names []string) policy.Finding {
	if len(names) == 0 {
		return profileContextFinding(
			"readiness.secret-provider", policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo,
			"This profile has no credential-staging helper prerequisite.", nil, nil,
		)
	}
	state := profilePrerequisitePass
	problem := profileProblemNone
	for _, name := range names {
		check := d.profileHelper(name)
		if check.State == profilePrerequisiteFail {
			state = profilePrerequisiteFail
			if check.Problem == profileProblemResolution {
				problem = profileProblemResolution
			} else if problem == profileProblemNone {
				problem = check.Problem
			}
		} else if check.State == profilePrerequisiteUnknown && state != profilePrerequisiteFail {
			state, problem = profilePrerequisiteUnknown, profileProblemCheck
		}
	}
	switch state {
	case profilePrerequisitePass:
		return profileContextFinding(
			"readiness.secret-provider", policy.FindingOutcomePass, policy.FindingSeverityInfo,
			fmt.Sprintf("%s available through verified sanitized-PATH resolution; no credential or secret value was resolved.", countHelpers(len(names))), nil, nil,
		)
	case profilePrerequisiteUnknown:
		return profileContextFinding(
			"readiness.secret-provider", policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
			"At least one required credential-staging helper could not be inspected; no secret value was resolved.", nil,
			contextRemediation(policy.RemediationKindRetryCheck, "retry-credential-helper-check", "Retry after sanitized helper inspection is available.", "#readiness"),
		)
	default:
		if problem == profileProblemResolution {
			return profileContextFinding(
				"readiness.secret-provider", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
				"At least one required credential-staging helper is shadowed or identity-unverified; launch remains blocked and no secret value was resolved.", nil,
				contextRemediation(policy.RemediationKindRepairHelperResolution, "repair-credential-helper-resolution", "Remove distinct helper shadows or repair sanitized PATH resolution.", "#readiness"),
			)
		}
		return profileContextFinding(
			"readiness.secret-provider", policy.FindingOutcomeFail, policy.FindingSeverityHigh,
			"At least one required credential-staging helper is unavailable; launch remains blocked and no secret value was resolved.", nil,
			contextRemediation(policy.RemediationKindInstallHelper, "install-credential-helper", "Install the required local helper or repair sanitized PATH.", "#readiness"),
		)
	}
}

func countHelpers(count int) string {
	if count == 1 {
		return "1 required credential-staging helper is"
	}
	return fmt.Sprintf("%d required credential-staging helpers are", count)
}

func toolchainFindingWithDeps(d *dependencies, prof policy.Profile) policy.Finding {
	if prof.Toolchain == nil || !toolchain.Wraps(prof.Toolchain.Kind) {
		return profileContextFinding(
			"readiness.toolchain.not-applicable", policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo,
			"This profile does not require a host toolchain helper.", nil, nil,
		)
	}
	ruleID := "readiness.toolchain." + prof.Toolchain.Kind
	check := d.profileHelper(prof.Toolchain.Kind)
	switch check.State {
	case profilePrerequisitePass:
		return profileContextFinding(
			ruleID, policy.FindingOutcomePass, policy.FindingSeverityInfo,
			"The declared toolchain helper has one verified sanitized-PATH resolution.", nil, nil,
		)
	case profilePrerequisiteUnknown:
		return profileContextFinding(
			ruleID, policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
			"The declared toolchain helper could not be inspected; launch readiness must not be assumed.", nil,
			contextRemediation(policy.RemediationKindRetryCheck, "retry-toolchain-helper-check", "Retry after sanitized helper inspection is available.", "#readiness"),
		)
	default:
		if check.Problem == profileProblemResolution {
			return profileContextFinding(
				ruleID, policy.FindingOutcomeFail, policy.FindingSeverityHigh,
				"The declared toolchain helper is shadowed or identity-unverified; launch remains blocked.", nil,
				contextRemediation(policy.RemediationKindRepairHelperResolution, "repair-toolchain-helper-resolution", "Remove distinct helper shadows or repair sanitized PATH resolution.", "#readiness"),
			)
		}
		return profileContextFinding(
			ruleID, policy.FindingOutcomeFail, policy.FindingSeverityHigh,
			"The declared toolchain helper is unavailable on sanitized PATH; launch remains blocked.", nil,
			contextRemediation(policy.RemediationKindInstallHelper, "install-toolchain-helper", "Install the declared toolchain helper or repair sanitized PATH.", "#readiness"),
		)
	}
}

func accountFindings(prof policy.Profile, checks profileAccountLinkChecks, scopes []policy.CredentialScope) []policy.Finding {
	credentials := prof.Credentials
	if credentials == nil {
		return nil
	}
	var findings []policy.Finding
	if credentials.Github != nil {
		if credentials.Github.Mode == "pat" {
			findings = append(findings, profileContextFinding(
				"readiness.github-account", policy.FindingOutcomeNotApplicable, policy.FindingSeverityInfo,
				"GitHub PAT mode does not use a host account link; secret resolution is outside this snapshot.", scopeIDsForProvider(scopes, policy.CredentialProviderGitHub), nil,
			))
		} else {
			findings = append(findings, providerAccountFinding("github", checks.Github, scopeIDsForProvider(scopes, policy.CredentialProviderGitHub)))
		}
	}
	if credentials.Forgejo != nil {
		findings = append(findings, providerAccountFinding("forgejo", checks.Forgejo, scopeIDsForProvider(scopes, policy.CredentialProviderForgejo)))
	}
	return findings
}

func providerAccountFinding(provider string, check profileProviderLinkCheck, scopeIDs []string) policy.Finding {
	ruleID := "readiness." + provider + "-account"
	label := "GitHub"
	if provider == "forgejo" {
		label = "Forgejo"
	}
	switch check.State {
	case profilePrerequisitePass:
		return profileContextFinding(
			ruleID, policy.FindingOutcomePass, policy.FindingSeverityInfo,
			fmt.Sprintf("Required %s account-link metadata is present for %s; refs and values were not resolved.", label, countAccountOwners(check.Count)), scopeIDs, nil,
		)
	case profilePrerequisiteFail:
		return profileContextFinding(
			ruleID, policy.FindingOutcomeFail, policy.FindingSeverityHigh,
			fmt.Sprintf("At least one required %s account link is absent; launch remains blocked and no account ref or value was resolved.", label), scopeIDs,
			contextRemediation(policy.RemediationKindOperatorWorkflow, "link-"+provider+"-account", "Add the required value-free account link, then retry.", "#readiness"),
		)
	default:
		return profileContextFinding(
			ruleID, policy.FindingOutcomeUnknown, policy.FindingSeverityHigh,
			fmt.Sprintf("Required %s account-link presence could not be determined locally; no account ref or value was resolved.", label), scopeIDs,
			contextRemediation(policy.RemediationKindRetryCheck, "retry-"+provider+"-account-check", "Retry after account-link metadata and origin context are available.", "#readiness"),
		)
	}
}

func countAccountOwners(count int) string {
	if count == 1 {
		return "1 declared owner"
	}
	return fmt.Sprintf("%d declared owners", count)
}

func scopeIDsForProvider(scopes []policy.CredentialScope, provider string) []string {
	var out []string
	for _, scope := range scopes {
		if scope.Provider == provider {
			out = append(out, scope.ScopeID)
		}
	}
	sort.Strings(out)
	if out == nil {
		return []string{}
	}
	return out
}

func profileContextFinding(ruleID, outcome, severity, consequence string, scopeIDs []string, remediation *policy.Remediation) policy.Finding {
	rule, ok := profileContextRulesByID[ruleID]
	if !ok {
		panic("unregistered profile context rule " + ruleID)
	}
	allowed := false
	for _, disposition := range rule.Allowed {
		if disposition.Outcome == outcome && disposition.Severity == severity {
			allowed = true
			break
		}
	}
	if !allowed {
		panic(fmt.Sprintf("invalid disposition %s/%s for profile context rule %s", outcome, severity, ruleID))
	}
	requiresRemediation := outcome == policy.FindingOutcomeFail || outcome == policy.FindingOutcomeUnknown || outcome == policy.FindingOutcomeConcern
	if requiresRemediation != (remediation != nil) {
		panic("invalid remediation presence for profile context rule " + ruleID)
	}
	if remediation != nil && !containsString(rule.RemediationKinds, remediation.Kind) {
		panic("invalid remediation kind for profile context rule " + ruleID)
	}
	if scopeIDs == nil {
		scopeIDs = []string{}
	} else {
		scopeIDs = append([]string(nil), scopeIDs...)
		sort.Strings(scopeIDs)
	}
	return policy.Finding{
		RuleID: rule.ID, Axis: rule.Axis, Outcome: outcome, Severity: severity,
		Title: rule.Title, Consequence: consequence, ScopeIDs: scopeIDs, Remediation: remediation,
	}
}

func contextRemediation(kind, actionID, summary, anchor string) *policy.Remediation {
	return &policy.Remediation{Kind: kind, ActionID: actionID, Summary: summary, DocsRef: profileEvaluationDocs + anchor}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
