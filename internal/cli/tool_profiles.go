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
	useSplit := len(cfg.ReadSplit) > 0 && profile != ToolProfileDescriptionsExecutor

	if cfg.ToolModes.Read == helper.ReadModeMCP {
		if useSplit {
			for kind := range cfg.ReadSplit {
				switch kind {
				case domain.ResourceFunction, domain.ResourceMethod:
					if allowed["read_function"] {
						lang := GetLanguage(manager)
						if lang == "python" {
							registry.Register(pythontools.NewReadFunction(python.NewPythonManager(manager)))
						} else {
							registry.Register(gotools.NewReadFunction(golang.NewGoManager(manager)))
						}
					}
				case domain.ResourceType:
					if allowed["read_struct"] {
						lang := GetLanguage(manager)
						if lang == "python" {
							registry.Register(pythontools.NewReadStruct(python.NewPythonManager(manager)))
						} else {
							registry.Register(gotools.NewReadStruct(golang.NewGoManager(manager)))
						}
					}
				case domain.ResourceInterface:
					if allowed["read_interface"] {
						lang := GetLanguage(manager)
						if lang == "python" {
							registry.Register(pythontools.NewReadInterface(python.NewPythonManager(manager)))
						} else {
							registry.Register(gotools.NewReadInterface(golang.NewGoManager(manager)))
						}
					}
				case domain.ResourceNamedType:
					if allowed["read_named_type"] {
						registry.Register(gotools.NewReadNamedType(golang.NewGoManager(manager)))
					}
				case domain.ResourceFile:
					if allowed["read_file"] {
						lang := GetLanguage(manager)
						if lang == "python" {
							registry.Register(pythontools.NewReadFile(python.NewPythonManager(manager)))
						} else {
							registry.Register(gotools.NewReadFile(golang.NewGoManager(manager)))
						}
					}
				case domain.ResourcePackage:
					if allowed["read_package"] {
						lang := GetLanguage(manager)
						if lang == "python" {
							registry.Register(pythontools.NewReadPackage(python.NewPythonManager(manager)))
						} else {
							registry.Register(gotools.NewReadPackage(golang.NewGoManager(manager)))
						}
					}
				case domain.ResourceDependency:
					if allowed["read_dependency"] {
						lang := GetLanguage(manager)
						if lang == "python" {
							registry.Register(pythontools.NewReadDependency(python.NewPythonManager(manager)))
						} else {
							registry.Register(gotools.NewReadDependency(golang.NewGoManager(manager)))
						}
					}
				}
			}
		} else if allowed["read"] {
			registry.Register(tools.NewRead(manager))
		}
	}
	if cfg.ToolModes.Edit == helper.EditModeMCP {
		if allowed["edit"] {
			registry.Register(tools.NewEdit(manager, scannerReg))
		}
		if allowed["write"] {
			registry.Register(tools.NewWrite(manager, scannerReg))
		}
	}
	if cfg.ToolModes.Grep == helper.GrepModeMCP && allowed["grep"] {
		registry.Register(tools.NewGrep(manager))
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
		return []string{"read", "grep", "edit", "write", "read_struct", "read_function", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency", "warnings_list", "bug_report", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete", "node_list_no_description", "update_description"}
	case ToolProfileDescriptionsExecutor:
		return []string{"read", "grep", "read_struct", "read_function", "read_interface", "read_file", "read_package", "read_dependency", "update_description"}
	case ToolProfileBugHunter:
		return []string{"read", "grep", "read_struct", "read_function", "read_interface", "read_file", "read_package", "read_dependency", "bug_report"}
	case ToolProfileBugJudge:
		return []string{"read", "grep", "read_struct", "read_function", "read_interface", "read_file", "read_package", "read_dependency", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case ToolProfileBugSolver:
		return []string{"read", "grep", "edit", "write", "read_struct", "read_function", "read_interface", "read_file", "read_package", "read_dependency", "bug_delete"}
	default:
		return []string{"read", "grep", "edit", "write", "read_struct", "read_function", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency", "warnings_list", "bug_report"}
	}
}

func registerGoTopologyTools(registry *tools.Registry, mgr *golang.GoManager, allowed map[string]bool, describeTargets []domain.ResourceKind) {
	if allowed["update_description"] {
		registry.Register(gotools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(gotools.NewNodeListNoDescription(mgr, describeTargets))
	}
}

func registerPythonTopologyTools(registry *tools.Registry, mgr *python.PythonManager, allowed map[string]bool, describeTargets []domain.ResourceKind) {
	if allowed["update_description"] {
		registry.Register(pythontools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(pythontools.NewNodeListNoDescription(mgr, describeTargets))
	}
}
