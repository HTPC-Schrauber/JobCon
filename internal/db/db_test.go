package db

import (
	"path/filepath"
	"testing"
)

func TestDBMigrationsAndCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Test Server CRUD
	server := &Server{
		ID:         "srv-01",
		Name:       "Test Server",
		Host:       "127.0.0.1",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/id_ed25519",
	}
	if err := database.CreateServer(server); err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	fetchedServer, err := database.GetServer("srv-01")
	if err != nil {
		t.Fatalf("failed to get server: %v", err)
	}
	if fetchedServer.Name != "Test Server" {
		t.Errorf("expected name 'Test Server', got %q", fetchedServer.Name)
	}
	if fetchedServer.KeepReleases != 3 {
		t.Errorf("expected keep_releases 3 (default), got %d", fetchedServer.KeepReleases)
	}

	fetchedServer.KeepReleases = 5
	if err := database.UpdateServer(fetchedServer); err != nil {
		t.Fatalf("failed to update server: %v", err)
	}
	updatedServer, err := database.GetServer("srv-01")
	if err != nil {
		t.Fatalf("failed to get updated server: %v", err)
	}
	if updatedServer.KeepReleases != 5 {
		t.Errorf("expected keep_releases 5, got %d", updatedServer.KeepReleases)
	}

	// Test Job CRUD
	job := &Job{
		ID:            "job-01",
		Name:          "sync_test",
		ServerID:      "srv-01",
		GroupID:       "com.example",
		ArtifactID:    "sync_test",
		ActiveVersion: "1.0.0",
		NexusRepo:     "releases",
	}
	if err := database.CreateJob(job); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	fetchedJob, err := database.GetJob("job-01")
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if fetchedJob.ActiveVersion != "1.0.0" {
		t.Errorf("expected active version '1.0.0', got %q", fetchedJob.ActiveVersion)
	}

	// Test Execution CRUD
	exec := &Execution{
		ID:          "exec-01",
		JobID:       "job-01",
		Action:      "run",
		Status:      "running",
		Version:     "1.0.0",
		LogPath:     "/tmp/exec-01.log",
		TriggeredBy: "test",
	}
	if err := database.CreateExecution(exec); err != nil {
		t.Fatalf("failed to create execution: %v", err)
	}

	activeCount, err := database.CountActiveExecutionsForJob("job-01")
	if err != nil {
		t.Fatalf("failed to count active executions: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("expected 1 active execution, got %d", activeCount)
	}

	exitCode := 0
	duration := int64(1500)
	if err := database.UpdateExecutionStatus("exec-01", "success", &exitCode, &duration); err != nil {
		t.Fatalf("failed to update execution status: %v", err)
	}

	updatedExec, err := database.GetExecution("exec-01")
	if err != nil {
		t.Fatalf("failed to get execution: %v", err)
	}
	if updatedExec.Status != "success" || *updatedExec.ExitCode != 0 {
		t.Errorf("unexpected execution state: status=%s, exit_code=%v", updatedExec.Status, updatedExec.ExitCode)
	}

	// Test ListJobsPaged
	paged, err := database.ListJobsPaged(JobFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("failed to list paged jobs: %v", err)
	}
	if paged.TotalCount != 1 {
		t.Errorf("expected 1 job, got %d", paged.TotalCount)
	}
	if len(paged.Jobs) != 1 || paged.Jobs[0].LastRunStatus != "success" {
		t.Errorf("expected job with LastRunStatus 'success', got %+v", paged.Jobs)
	}

	// Test User Preferences
	_ = database.CreateUser(&User{ID: "u-01", Username: "prefuser", PasswordHash: "hash", DisplayName: "Pref User", Role: "viewer"})
	if err := database.SetUserPreference("u-01", "page_size", "50"); err != nil {
		t.Fatalf("failed to set user preference: %v", err)
	}
	val, err := database.GetUserPreference("u-01", "page_size", "10")
	if err != nil || val != "50" {
		t.Errorf("expected '50', got %q (err: %v)", val, err)
	}
}

func TestListJobsPagedGroupingAndSorting(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := Open(filepath.Join(tmpDir, "test_grouping.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	_ = database.CreateServer(&Server{ID: "srv-01", Name: "Server 1", Host: "127.0.0.1", User: "talend"})

	// Create jobs across two groups:
	// group_b: Alpha, Gamma
	// group_a: Zeta, Beta
	jobs := []*Job{
		{ID: "j1", Name: "Alpha", ServerID: "srv-01", GroupID: "group_b", ArtifactID: "art1", ActiveVersion: "1.0", NexusRepo: "releases"},
		{ID: "j2", Name: "Zeta", ServerID: "srv-01", GroupID: "group_a", ArtifactID: "art2", ActiveVersion: "1.0", NexusRepo: "releases"},
		{ID: "j3", Name: "Beta", ServerID: "srv-01", GroupID: "group_a", ArtifactID: "art3", ActiveVersion: "1.0", NexusRepo: "releases"},
		{ID: "j4", Name: "Gamma", ServerID: "srv-01", GroupID: "group_b", ArtifactID: "art4", ActiveVersion: "1.0", NexusRepo: "releases"},
	}
	for _, j := range jobs {
		if err := database.CreateJob(j); err != nil {
			t.Fatalf("failed to create job %s: %v", j.ID, err)
		}
	}

	// 1. Without grouping, sort by Name ASC: Alpha, Beta, Gamma, Zeta
	resNoGroup, err := database.ListJobsPaged(JobFilter{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("failed to list jobs: %v", err)
	}
	var namesNoGroup []string
	for _, j := range resNoGroup.Jobs {
		namesNoGroup = append(namesNoGroup, j.Name)
	}
	expectedNoGroup := []string{"Alpha", "Beta", "Gamma", "Zeta"}
	for i, name := range expectedNoGroup {
		if namesNoGroup[i] != name {
			t.Errorf("expected no-group[%d] = %s, got %s", i, name, namesNoGroup[i])
		}
	}

	// 2. With grouping (group_id), sort by Name ASC:
	// group_a: Beta, Zeta
	// group_b: Alpha, Gamma
	resGroupAsc, err := database.ListJobsPaged(JobFilter{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "asc", GroupBy: "group_id"})
	if err != nil {
		t.Fatalf("failed to list jobs with grouping: %v", err)
	}
	var namesGroupAsc []string
	for _, j := range resGroupAsc.Jobs {
		namesGroupAsc = append(namesGroupAsc, j.Name)
	}
	expectedGroupAsc := []string{"Beta", "Zeta", "Alpha", "Gamma"}
	for i, name := range expectedGroupAsc {
		if namesGroupAsc[i] != name {
			t.Errorf("expected group-asc[%d] = %s, got %s", i, name, namesGroupAsc[i])
		}
	}

	// 3. With grouping (group_id), sort by Name DESC:
	// group_a: Zeta, Beta
	// group_b: Gamma, Alpha
	resGroupDesc, err := database.ListJobsPaged(JobFilter{Page: 1, PageSize: 10, SortBy: "name", SortOrder: "desc", GroupBy: "group_id"})
	if err != nil {
		t.Fatalf("failed to list jobs with grouping desc: %v", err)
	}
	var namesGroupDesc []string
	for _, j := range resGroupDesc.Jobs {
		namesGroupDesc = append(namesGroupDesc, j.Name)
	}
	expectedGroupDesc := []string{"Zeta", "Beta", "Gamma", "Alpha"}
	for i, name := range expectedGroupDesc {
		if namesGroupDesc[i] != name {
			t.Errorf("expected group-desc[%d] = %s, got %s", i, name, namesGroupDesc[i])
		}
	}
}

func TestServerDeletionAndReassignment(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_server_del.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	s1 := &Server{
		ID:         "srv-del-1",
		Name:       "Server 1",
		Host:       "10.0.0.1",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/k1",
		JobsDir:    "/custom/jobs",
		ScriptsDir: "/custom/scripts",
	}
	s2 := &Server{
		ID:         "srv-del-2",
		Name:       "Server 2",
		Host:       "10.0.0.2",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/k2",
	}
	if err := database.CreateServer(s1); err != nil {
		t.Fatalf("create s1 failed: %v", err)
	}
	if err := database.CreateServer(s2); err != nil {
		t.Fatalf("create s2 failed: %v", err)
	}

	fetchedS1, err := database.GetServer("srv-del-1")
	if err != nil {
		t.Fatalf("get s1 failed: %v", err)
	}
	if fetchedS1.JobsDir != "/custom/jobs" || fetchedS1.ScriptsDir != "/custom/scripts" {
		t.Errorf("expected custom dirs, got jobs=%s scripts=%s", fetchedS1.JobsDir, fetchedS1.ScriptsDir)
	}

	fetchedS2, err := database.GetServer("srv-del-2")
	if err != nil {
		t.Fatalf("get s2 failed: %v", err)
	}
	if fetchedS2.JobsDir != "/opt/talend/jobs" || fetchedS2.ScriptsDir != "/opt/talend/scripts" {
		t.Errorf("expected default dirs, got jobs=%s scripts=%s", fetchedS2.JobsDir, fetchedS2.ScriptsDir)
	}

	// Create job attached to srv-del-1
	j1 := &Job{
		ID:            "job-del-1",
		Name:          "Job Del 1",
		ServerID:      "srv-del-1",
		GroupID:       "grp",
		ArtifactID:    "art",
		ActiveVersion: "1.0",
		NexusRepo:     "releases",
	}
	if err := database.CreateJob(j1); err != nil {
		t.Fatalf("create j1 failed: %v", err)
	}

	// GetJobsByServerID
	jobsOnS1, err := database.GetJobsByServerID("srv-del-1")
	if err != nil {
		t.Fatalf("GetJobsByServerID failed: %v", err)
	}
	if len(jobsOnS1) != 1 || jobsOnS1[0].ID != "job-del-1" {
		t.Fatalf("expected job-del-1, got %+v", jobsOnS1)
	}

	// Direct delete should fail with ErrServerInUse
	if err := database.DeleteServer("srv-del-1"); err != ErrServerInUse {
		t.Fatalf("expected ErrServerInUse, got %v", err)
	}

	// Delete with reassignment to srv-del-2
	if err := database.DeleteServerWithJobReassignment("srv-del-1", "srv-del-2"); err != nil {
		t.Fatalf("DeleteServerWithJobReassignment failed: %v", err)
	}

	// Verify srv-del-1 is deleted
	if _, err := database.GetServer("srv-del-1"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound for srv-del-1, got %v", err)
	}

	// Verify job is now assigned to srv-del-2
	updatedJob, err := database.GetJob("job-del-1")
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if updatedJob.ServerID != "srv-del-2" {
		t.Errorf("expected job server_id srv-del-2, got %s", updatedJob.ServerID)
	}

	// Now delete srv-del-2 with reassignment without target -> should fail because job is attached
	if err := database.DeleteServerWithJobReassignment("srv-del-2", ""); err != ErrServerInUse {
		t.Errorf("expected ErrServerInUse for srv-del-2 without target, got %v", err)
	}
}

