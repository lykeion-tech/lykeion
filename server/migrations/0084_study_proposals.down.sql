DROP TRIGGER detach_project_proposals ON research;
DROP TRIGGER retire_departed_proposals ON member;
DROP FUNCTION detach_project_proposals(), retire_departed_proposals();
DROP TABLE study_proposal;
