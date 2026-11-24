DROP INDEX study_run_stage_round;
DROP TRIGGER study_run_worker_at_enqueue ON agent_run;
DROP FUNCTION snapshot_study_run_worker();
DROP TABLE study_run_worker;
