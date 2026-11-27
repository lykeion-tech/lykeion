-- The lesson writer that consumed these jobs was removed; nothing queues or reads them.
DROP FUNCTION pending_lesson_batch(), due_lesson_jobs(int);
DROP TABLE lesson_job_input, lesson_job;
