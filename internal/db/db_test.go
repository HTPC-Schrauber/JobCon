package db

import (
	"path/filepath"
	"strings"
	"testing"

	"jobcon/internal/crypto"
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

	// Test HostKey update and reset
	if fetchedServer.HostKey != "" {
		t.Errorf("expected empty initial host_key, got %q", fetchedServer.HostKey)
	}
	testKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGabcdef1234567890"
	if err := database.UpdateServerHostKey("srv-01", testKey); err != nil {
		t.Fatalf("failed to update server host key: %v", err)
	}
	withKey, err := database.GetServer("srv-01")
	if err != nil {
		t.Fatalf("failed to get server after host key update: %v", err)
	}
	if withKey.HostKey != testKey {
		t.Errorf("expected host_key %q, got %q", testKey, withKey.HostKey)
	}

	if err := database.ResetServerHostKey("srv-01"); err != nil {
		t.Fatalf("failed to reset server host key: %v", err)
	}
	resetServer, err := database.GetServer("srv-01")
	if err != nil {
		t.Fatalf("failed to get server after host key reset: %v", err)
	}
	if resetServer.HostKey != "" {
		t.Errorf("expected empty host_key after reset, got %q", resetServer.HostKey)
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

	// Test SetJobDeployed
	if err := database.SetJobDeployed("job-01", true, "1.2.3"); err != nil {
		t.Fatalf("failed to set job deployed: %v", err)
	}
	jobCheck, err := database.GetJob("job-01")
	if err != nil || !jobCheck.IsDeployed || jobCheck.DeployedVersion != "1.2.3" {
		t.Errorf("expected job to be deployed v1.2.3, got is_deployed=%v, ver=%s (err: %v)", jobCheck.IsDeployed, jobCheck.DeployedVersion, err)
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

func TestGetJobsByArtifact(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	_ = database.CreateServer(&Server{ID: "srv-1", Name: "Server 1", Host: "10.0.0.1", SSHKeyPath: "/tmp/key"})
	_ = database.CreateServer(&Server{ID: "srv-2", Name: "Server 2", Host: "10.0.0.2", SSHKeyPath: "/tmp/key"})

	j1 := &Job{ID: "job-1", Name: "Job 1", ServerID: "srv-1", GroupID: "grp", ArtifactID: "art-x", ActiveVersion: "1.0", NexusRepo: "r"}
	j2 := &Job{ID: "job-2", Name: "Job 2", ServerID: "srv-1", GroupID: "grp", ArtifactID: "art-x", ActiveVersion: "2.0", NexusRepo: "r"}
	j3 := &Job{ID: "job-3", Name: "Job 3", ServerID: "srv-2", GroupID: "grp", ArtifactID: "art-x", ActiveVersion: "1.0", NexusRepo: "r"}
	j4 := &Job{ID: "job-4", Name: "Job 4", ServerID: "srv-2", GroupID: "grp", ArtifactID: "art-y", ActiveVersion: "1.0", NexusRepo: "r"}

	_ = database.CreateJob(j1)
	_ = database.CreateJob(j2)
	_ = database.CreateJob(j3)
	_ = database.CreateJob(j4)

	jobsX, err := database.GetJobsByArtifact("art-x")
	if err != nil {
		t.Fatalf("GetJobsByArtifact failed: %v", err)
	}
	if len(jobsX) != 3 {
		t.Errorf("expected 3 jobs for art-x, got %d", len(jobsX))
	}

	jobsOnSrv1, err := database.GetJobsByArtifactOnServer("art-x", "srv-1")
	if err != nil {
		t.Fatalf("GetJobsByArtifactOnServer failed: %v", err)
	}
	if len(jobsOnSrv1) != 2 {
		t.Errorf("expected 2 jobs for art-x on srv-1, got %d", len(jobsOnSrv1))
	}

	jobsOnSrv2, err := database.GetJobsByArtifactOnServer("art-x", "srv-2")
	if err != nil {
		t.Fatalf("GetJobsByArtifactOnServer failed: %v", err)
	}
	if len(jobsOnSrv2) != 1 {
		t.Errorf("expected 1 job for art-x on srv-2, got %d", len(jobsOnSrv2))
	}
}

func TestNexusArtifactsCacheAndCounts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Initial counts
	online, err := database.CountOnlineServers()
	if err != nil || online != 0 {
		t.Fatalf("expected 0 online servers, got %d (err: %v)", online, err)
	}
	active, err := database.CountActiveExecutions()
	if err != nil || active != 0 {
		t.Fatalf("expected 0 active executions, got %d (err: %v)", active, err)
	}

	// Add servers and verify count
	_ = database.CreateServer(&Server{ID: "s1", Name: "S1", Host: "1.1.1.1", SSHKeyPath: "/k", Status: "online"})
	_ = database.CreateServer(&Server{ID: "s2", Name: "S2", Host: "1.1.1.2", SSHKeyPath: "/k", Status: "offline"})
	online, _ = database.CountOnlineServers()
	if online != 1 {
		t.Errorf("expected 1 online server, got %d", online)
	}

	// Add execution in pending state
	_ = database.CreateJob(&Job{ID: "j1", Name: "J1", ServerID: "s1", GroupID: "g", ArtifactID: "a", ActiveVersion: "1.0", NexusRepo: "r"})
	_ = database.CreateExecution(&Execution{ID: "e1", JobID: "j1", Action: "run", Status: "pending", Version: "1.0", TriggeredBy: "test"})
	active, _ = database.CountActiveExecutions()
	if active != 1 {
		t.Errorf("expected 1 active execution, got %d", active)
	}

	// Transition to running
	if err := database.SetExecutionRunning("e1"); err != nil {
		t.Fatalf("SetExecutionRunning failed: %v", err)
	}
	e, _ := database.GetExecution("e1")
	if e.Status != "running" {
		t.Errorf("expected status 'running', got %s", e.Status)
	}

	// Test Nexus artifacts cache
	comps := []SyncedComponent{
		{Group: "com.opitzhome.jobs", Name: "invoice_export", Version: "1.0.0"},
		{Group: "com.opitzhome.jobs", Name: "invoice_export", Version: "1.1.0"},
		{Group: "com.opitzhome.jobs", Name: "inventory_update", Version: "2.0.0"},
		{Group: "com.opitzhome.test", Name: "echo_test", Version: "0.9.0"},
	}

	if err := database.ReplaceNexusArtifacts("releases", comps); err != nil {
		t.Fatalf("ReplaceNexusArtifacts failed: %v", err)
	}

	count, err := database.GetNexusArtifactCount()
	if err != nil || count != 4 {
		t.Fatalf("expected 4 artifacts, got %d (err: %v)", count, err)
	}

	// Search by query
	nodes, err := database.SearchNexusArtifacts("releases", "", "", "invoice")
	if err != nil {
		t.Fatalf("SearchNexusArtifacts failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 group node, got %d", len(nodes))
	}
	if nodes[0].Group != "com.opitzhome.jobs" {
		t.Errorf("expected group com.opitzhome.jobs, got %s", nodes[0].Group)
	}
	if len(nodes[0].Artifacts) != 1 || nodes[0].Artifacts[0].ArtifactID != "invoice_export" {
		t.Fatalf("expected artifact invoice_export, got %+v", nodes[0].Artifacts)
	}
	if len(nodes[0].Artifacts[0].Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(nodes[0].Artifacts[0].Versions))
	}
	if nodes[0].Artifacts[0].Versions[0] != "1.1.0" {
		t.Errorf("expected newest version 1.1.0 first, got %s", nodes[0].Artifacts[0].Versions[0])
	}
}

func TestLocalAdminProtectionAndLDAPUsers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_admin.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Initially 0 users
	admins, err := database.CountActiveLocalAdmins()
	if err != nil || admins != 0 {
		t.Fatalf("expected 0 active local admins, got %d (err: %v)", admins, err)
	}

	// Create local admin 1
	admin1 := &User{
		ID:           "u-admin-1",
		Username:     "localadmin1",
		PasswordHash: "hash1",
		DisplayName:  "Local Admin 1",
		Email:        "admin1@jobcon.local",
		Role:         "admin",
		AuthSource:   "local",
		IsActive:     true,
	}
	if err := database.CreateUser(admin1); err != nil {
		t.Fatalf("failed to create admin1: %v", err)
	}

	// Now 1 active local admin
	admins, err = database.CountActiveLocalAdmins()
	if err != nil || admins != 1 {
		t.Fatalf("expected 1 active local admin, got %d", admins)
	}

	// Case-insensitive lookup
	fetched, err := database.GetUserByUsername("LocalAdmin1")
	if err != nil || fetched.ID != "u-admin-1" {
		t.Fatalf("case-insensitive lookup failed: %v", err)
	}

	// Create LDAP user
	ldapUser := &User{
		ID:           "u-ldap-1",
		Username:     "ldapuser1",
		PasswordHash: "",
		DisplayName:  "LDAP User 1",
		Email:        "ldapuser1@firma.de",
		Role:         "viewer",
		AuthSource:   "ldap",
		IsActive:     true,
	}
	if err := database.CreateUser(ldapUser); err != nil {
		t.Fatalf("failed to create ldap user: %v", err)
	}

	// Active local admins count should still be 1
	admins, _ = database.CountActiveLocalAdmins()
	if admins != 1 {
		t.Fatalf("expected 1 active local admin after creating ldap user, got %d", admins)
	}

	// Trying to delete admin1 must fail because of ErrLastAdminProtection
	if err := database.DeleteUser("u-admin-1"); err != ErrLastAdminProtection {
		t.Fatalf("expected ErrLastAdminProtection when deleting last admin, got %v", err)
	}

	// Trying to demote admin1 to viewer must fail
	admin1.Role = "viewer"
	if err := database.UpdateUser(admin1); err != ErrLastAdminProtection {
		t.Fatalf("expected ErrLastAdminProtection when demoting last admin, got %v", err)
	}

	// Trying to deactivate admin1 must fail
	admin1.Role = "admin"
	admin1.IsActive = false
	if err := database.UpdateUser(admin1); err != ErrLastAdminProtection {
		t.Fatalf("expected ErrLastAdminProtection when deactivating last admin, got %v", err)
	}

	// Trying to switch admin1 to ldap must fail
	admin1.IsActive = true
	admin1.AuthSource = "ldap"
	if err := database.UpdateUser(admin1); err != ErrLastAdminProtection {
		t.Fatalf("expected ErrLastAdminProtection when switching last admin to ldap, got %v", err)
	}

	// Create local admin 2
	admin2 := &User{
		ID:           "u-admin-2",
		Username:     "localadmin2",
		PasswordHash: "hash2",
		DisplayName:  "Local Admin 2",
		Email:        "admin2@jobcon.local",
		Role:         "admin",
		AuthSource:   "local",
		IsActive:     true,
	}
	if err := database.CreateUser(admin2); err != nil {
		t.Fatalf("failed to create admin2: %v", err)
	}

	admins, _ = database.CountActiveLocalAdmins()
	if admins != 2 {
		t.Fatalf("expected 2 active local admins, got %d", admins)
	}

	// Now admin1 CAN be demoted or deleted because admin2 exists
	admin1.AuthSource = "local"
	admin1.Role = "operator"
	if err := database.UpdateUser(admin1); err != nil {
		t.Fatalf("failed to demote admin1 when admin2 exists: %v", err)
	}

	// Now admin2 is the last remaining local admin and must be protected
	admins, _ = database.CountActiveLocalAdmins()
	if admins != 1 {
		t.Fatalf("expected 1 active local admin, got %d", admins)
	}

	if err := database.DeleteUser("u-admin-2"); err != ErrLastAdminProtection {
		t.Fatalf("expected ErrLastAdminProtection when deleting admin2, got %v", err)
	}

	// LDAP user can be deleted without issue
	if err := database.DeleteUser("u-ldap-1"); err != nil {
		t.Fatalf("failed to delete ldap user: %v", err)
	}
}

func TestEncryptedSettings(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_crypto.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// Initialize crypto with a known 32-byte key
	crypto.Init("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	// 1. Setting and getting encrypted value
	plainSecret := "SuperSecretPassword123!"
	if err := database.SetEncryptedSetting("nexus_password", plainSecret); err != nil {
		t.Fatalf("failed to set encrypted setting: %v", err)
	}

	// Raw setting in DB must start with "enc:v1:"
	rawVal, err := database.GetSetting("nexus_password", "")
	if err != nil {
		t.Fatalf("failed to get raw setting: %v", err)
	}
	if !strings.HasPrefix(rawVal, "enc:v1:") {
		t.Errorf("expected raw value to be prefixed with 'enc:v1:', got %q", rawVal)
	}

	// GetEncryptedSetting must return the decrypted plaintext
	gotSecret, err := database.GetEncryptedSetting("nexus_password", "")
	if err != nil {
		t.Fatalf("failed to get encrypted setting: %v", err)
	}
	if gotSecret != plainSecret {
		t.Errorf("expected decrypted secret %q, got %q", plainSecret, gotSecret)
	}

	// 2. Legacy plaintext fallback:
	// If a setting was saved without encryption in an older version, GetEncryptedSetting should return it directly
	if err := database.SetSetting("ldap_bind_password", "PlainOldPassword"); err != nil {
		t.Fatalf("failed to set legacy setting: %v", err)
	}
	legacyVal, err := database.GetEncryptedSetting("ldap_bind_password", "")
	if err != nil {
		t.Fatalf("failed to get legacy setting: %v", err)
	}
	if legacyVal != "PlainOldPassword" {
		t.Errorf("expected legacy secret 'PlainOldPassword', got %q", legacyVal)
	}

	// 3. Default value fallback
	defVal, err := database.GetEncryptedSetting("non_existing_key", "default_val")
	if err != nil {
		t.Fatalf("unexpected error on default fallback: %v", err)
	}
	if defVal != "default_val" {
		t.Errorf("expected 'default_val', got %q", defVal)
	}

	// 4. Empty value storage
	if err := database.SetEncryptedSetting("nexus_password", ""); err != nil {
		t.Fatalf("failed to set empty encrypted setting: %v", err)
	}
	emptyVal, err := database.GetEncryptedSetting("nexus_password", "none")
	if err != nil {
		t.Fatalf("failed to get empty encrypted setting: %v", err)
	}
	if emptyVal != "" {
		t.Errorf("expected empty string, got %q", emptyVal)
	}
}


