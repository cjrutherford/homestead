package docker

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	"homestead/internal/layout"
)

// Compose label keys identifying stack membership.
const (
	// LabelProject marks the Compose project (stack name).
	LabelProject = "com.docker.compose.project"
	// LabelService marks the Compose service.
	LabelService = "com.docker.compose.service"
	// LabelOneOff marks `docker compose run` containers ("True").
	LabelOneOff = "com.docker.compose.oneoff"
)

var validStackName = regexp.MustCompile(`^[a-z0-9_-]+$`)
var validServiceName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Stack is the desired state of one Compose application: where its file
// lives, which services should run (empty means all in the file), and
// whether it should run at all. Service configuration itself lives in
// the Compose file, operable without Homestead (ADR-005).
type Stack struct {
	Name     string
	Dir      string
	Services []string
	Enabled  bool
}

// DirFor returns the stack directory under the layout stacks root.
func DirFor(name string) string {
	return filepath.Join(layout.StacksDir.Path, name)
}

// Validate checks naming, directory placement, and service names.
func (s Stack) Validate() error {
	if !validStackName.MatchString(s.Name) {
		return fmt.Errorf("stack: invalid name %q", s.Name)
	}
	if s.Dir == "" || s.Dir != DirFor(s.Name) {
		return fmt.Errorf("stack: dir must be %q", DirFor(s.Name))
	}
	for _, service := range s.Services {
		if !validServiceName.MatchString(service) {
			return fmt.Errorf("stack: invalid service %q", service)
		}
	}
	return nil
}

// ObservedService is one Compose service as actually running.
type ObservedService struct {
	Name       string
	Running    int
	Total      int
	Wanted     bool
	Containers []Container
}

// ObservedStack is one Compose project as actually running.
type ObservedStack struct {
	Name     string
	Services []ObservedService
	Running  bool
}

// ObserveStacks groups Engine containers into stacks by Compose labels.
// One-off `run` containers and unlabeled containers are not stack state
// and are skipped. Wanted marks services in the desired set (empty
// desired means every observed service is wanted).
func ObserveStacks(containers []Container, desired []string) []ObservedStack {
	want := map[string]bool{}
	for _, name := range desired {
		want[name] = true
	}
	byStack := map[string]map[string]*ObservedService{}
	var order []string
	for _, container := range containers {
		project, ok := container.Labels[LabelProject]
		if !ok || project == "" {
			continue
		}
		service, ok := container.Labels[LabelService]
		if !ok || service == "" {
			continue
		}
		if container.Labels[LabelOneOff] == "True" {
			continue
		}
		services, ok := byStack[project]
		if !ok {
			services = map[string]*ObservedService{}
			byStack[project] = services
			order = append(order, project)
		}
		entry, ok := services[service]
		if !ok {
			entry = &ObservedService{Name: service, Wanted: len(want) == 0 || want[service]}
			services[service] = entry
		}
		entry.Total++
		if container.State == "running" {
			entry.Running++
		}
		entry.Containers = append(entry.Containers, container)
	}
	stacks := make([]ObservedStack, 0, len(order))
	sort.Strings(order)
	for _, project := range order {
		services := byStack[project]
		names := make([]string, 0, len(services))
		for name := range services {
			names = append(names, name)
		}
		sort.Strings(names)
		stack := ObservedStack{Name: project}
		for _, name := range names {
			entry := services[name]
			stack.Services = append(stack.Services, *entry)
			if entry.Running > 0 {
				stack.Running = true
			}
		}
		stacks = append(stacks, stack)
	}
	return stacks
}
