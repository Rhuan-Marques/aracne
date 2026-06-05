package cli

import (
	"fmt"

	"ltp/internal/helper"
	"ltp/internal/llm/languages/gotools"
	"ltp/internal/llm/languages/pythontools"
	"ltp/internal/llm/tools"
	"ltp/internal/topology"
	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
	"ltp/internal/topology/python"
	"ltp/internal/topology/scanner"
)

type ToolProfile string

const (
	ToolProfileAll                  ToolProfile = "all"
	ToolProfileDefault              ToolProfile = "default"
	ToolProfileDescriptionsExecutor ToolProfile = "descriptions-executor"
	ToolProfileBugHunter            ToolProfile = "bug-hunter"
	ToolProfileBugJudge             ToolProfile = "bug-judge"
	ToolProfileBugSolver            ToolProfile = "bug-solver"
)

func ParseToolProfile(value string) (ToolProfile, error) {
	if value == "" {
		return ToolProfileDefault, nil
	}
	switch ToolProfile(value) {
	case ToolProfileAll, ToolProfileDefault, ToolProfileDescriptionsExecutor, ToolProfileBugHunter, ToolProfileBugJudge, ToolProfileBugSolver:
		return ToolProfile(value), nil
	default:
		return "", fmt.Errorf("invalid tool profile %q", value)
	}
}

func BuildToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, profile ToolProfile) *tools.Registry {
	registry := tools.NewRegistry()
	allowed := allowedToolsForProfile(profile)

	if cfg.ToolModes.Read == helper.ReadModeMCP && allowed["read_file"] {
		registry.Register(&tools.ReadFile{})
	}
	if cfg.ToolModes.Edit == helper.EditModeMCP {
		if allowed["edit"] {
			registry.Register(tools.NewEdit(manager, scannerReg))
		}
		if allowed["write"] {
			registry.Register(tools.NewWrite(manager, scannerReg))
		}
	}
	if cfg.ToolModes.Other != helper.OtherModeMCP {
		return registry
	}

	if allowed["warnings_list"] {
		registry.Register(tools.NewWarningsList(manager))
	}
	if allowed["bug_report"] {
		registry.Register(tools.NewBugReport(manager))
	}
	if allowed["bug_list"] {
		registry.Register(tools.NewBugList(manager))
	}
	if allowed["bug_acknowledge"] {
		registry.Register(tools.NewBugAcknowledge(manager))
	}
	if allowed["bug_dismiss"] {
		registry.Register(tools.NewBugDismiss(manager))
	}
	if allowed["bug_delete"] {
		registry.Register(tools.NewBugDelete(manager))
	}

	lang := GetLanguage(manager)
	if lang == "python" {
		registerPythonTopologyTools(registry, python.NewPythonManager(manager), allowed, cfg.DescribeTargets)
		return registry
	}
	registerGoTopologyTools(registry, golang.NewGoManager(manager), allowed, cfg.DescribeTargets)
	return registry
}

func allowedToolsForProfile(profile ToolProfile) map[string]bool {
	tools := map[string]bool{}
	for _, name := range profileTools(profile) {
		tools[name] = true
	}
	return tools
}

func profileTools(profile ToolProfile) []string {
	switch profile {
	case ToolProfileAll:
		return []string{"read_file", "edit", "write", "read_struct", "read_function", "warnings_list", "bug_report", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete", "node_list_no_description", "read_resource_and_cut", "update_description"}
	case ToolProfileDescriptionsExecutor:
		return []string{"read_file", "read_struct", "read_function", "read_resource_and_cut", "update_description"}
	case ToolProfileBugHunter:
		return []string{"read_file", "read_struct", "read_function", "bug_report"}
	case ToolProfileBugJudge:
		return []string{"read_file", "read_struct", "read_function", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case ToolProfileBugSolver:
		return []string{"read_file", "edit", "write", "read_struct", "read_function", "bug_delete"}
	default:
		return []string{"read_file", "edit", "write", "read_struct", "read_function", "warnings_list", "bug_report"}
	}
}

func registerGoTopologyTools(registry *tools.Registry, mgr *golang.GoManager, allowed map[string]bool, describeTargets []domain.ResourceKind) {
	if allowed["read_function"] {
		registry.Register(gotools.NewReadFunction(mgr))
	}
	if allowed["read_struct"] {
		registry.Register(gotools.NewReadStruct(mgr))
	}
	if allowed["read_resource_and_cut"] {
		registry.Register(gotools.NewReadResourceAndCut(mgr))
	}
	if allowed["update_description"] {
		registry.Register(gotools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(gotools.NewNodeListNoDescription(mgr, describeTargets))
	}
}

func registerPythonTopologyTools(registry *tools.Registry, mgr *python.PythonManager, allowed map[string]bool, describeTargets []domain.ResourceKind) {
	if allowed["read_function"] {
		registry.Register(pythontools.NewReadFunction(mgr))
	}
	if allowed["read_struct"] {
		registry.Register(pythontools.NewReadStruct(mgr))
	}
	if allowed["read_resource_and_cut"] {
		registry.Register(pythontools.NewReadResourceAndCut(mgr))
	}
	if allowed["update_description"] {
		registry.Register(pythontools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(pythontools.NewNodeListNoDescription(mgr, describeTargets))
	}
}
