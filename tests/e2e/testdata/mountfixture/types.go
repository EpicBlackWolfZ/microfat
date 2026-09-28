// Package mountfixture defines the test-only protocol shared by native mount
// qualification and its subprocess helpers. It is not a product API.
package mountfixture

type Mount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Request struct {
	Binfmt             *Binfmt  `json:"binfmt,omitempty"`
	Root               string   `json:"root"`
	UID                int      `json:"uid"`
	GID                int      `json:"gid"`
	ParentNamespace    string   `json:"parent_namespace"`
	ParentPIDNamespace string   `json:"parent_pid_namespace"`
	Mounts             []Mount  `json:"mounts"`
	Proc               string   `json:"proc"`
	ReadOnlyRoot       bool     `json:"read_only_root"`
	Command            string   `json:"command"`
	Args               []string `json:"args"`
	Env                []string `json:"env"`
	Stdin              string   `json:"stdin"`
	Runs               int      `json:"runs"`
	Pause              bool     `json:"pause"`
	Renames            []Rename `json:"renames,omitempty"`
	Remove             []string `json:"remove,omitempty"`
}

// Binfmt is restricted to the isolated F-only ARM64 qualification fixture.
type Binfmt struct {
	ParentUserNamespace string `json:"parent_user_namespace"`
	Preflight           bool   `json:"preflight,omitempty"`
}

type Execution struct {
	PID      int    `json:"pid"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Started  string `json:"started"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

type Result struct {
	UserNamespace string      `json:"user_namespace,omitempty"`
	Registration  string      `json:"registration,omitempty"`
	Prerequisite  string      `json:"prerequisite,omitempty"`
	Schema        int         `json:"schema"`
	Stage         string      `json:"stage"`
	Error         string      `json:"error,omitempty"`
	Namespace     string      `json:"namespace"`
	PIDNamespace  string      `json:"pid_namespace"`
	UIDMap        string      `json:"uid_map"`
	GIDMap        string      `json:"gid_map"`
	MountInfo     string      `json:"mountinfo"`
	Executions    []Execution `json:"executions"`
}

// Process identifies a test process independently of host PID numbering.
type Process struct {
	Token     string `json:"token,omitempty"`
	PID       int    `json:"pid"`
	Namespace string `json:"pid_namespace"`
}

type Report struct {
	ProbeFDTarget     string            `json:"probe_fd_target,omitempty"`
	NoNewPrivileges   string            `json:"no_new_privileges,omitempty"`
	Identity          string            `json:"identity"`
	Digest            string            `json:"digest"`
	PID               int               `json:"pid"`
	WorkingDirectory  string            `json:"working_directory"`
	UID               int               `json:"uid"`
	EUID              int               `json:"euid"`
	GID               int               `json:"gid"`
	EGID              int               `json:"egid"`
	Args              []string          `json:"args"`
	Stdin             string            `json:"stdin"`
	Environment       string            `json:"environment"`
	Mode              string            `json:"mode"`
	Original          string            `json:"original"`
	Executable        string            `json:"executable"`
	RuntimeExecutable string            `json:"runtime_executable"`
	Asset             string            `json:"asset"`
	ExplicitAsset     string            `json:"explicit_asset"`
	Capabilities      string            `json:"capabilities"`
	Errors            map[string]string `json:"errors"`
}

type DescriptorProbe struct {
	Target  string `json:"target"`
	Event   string `json:"event"`
	PID     int    `json:"pid"`
	FD      int    `json:"fd"`
	Flags   int    `json:"flags"`
	Seals   int    `json:"seals"`
	Storage string `json:"storage"`
	Digest  string `json:"digest"`
	Error   string `json:"error,omitempty"`
}
