DELETE FROM inbox_item WHERE kind IN ('study_report','autoresearch_failed');
ALTER TABLE inbox_item DROP CONSTRAINT inbox_item_kind_check,
 ADD CONSTRAINT inbox_item_kind_check CHECK (kind IN
 ('autoresearch_card','autoresearch_report','middle_score','blocked','no_tool','scoring_unavailable','done','waiting_for_machine','needs_decision','open_doubts','agent_decision','machine_offline','manual_decision'));
