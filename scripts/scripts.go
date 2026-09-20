package scripts

import _ "embed"

//go:embed jobcon_ctl.sh
var JobconCtlSh []byte

//go:embed run_job.sh
var RunJobSh []byte
