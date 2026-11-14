DO $$
BEGIN
 LOCK TABLE decision_draft_receipt IN ACCESS EXCLUSIVE MODE;
 IF EXISTS (SELECT 1 FROM decision_draft_receipt) THEN
  RAISE EXCEPTION 'Cannot remove Decision creation receipts while they exist';
 END IF;
 DROP TABLE decision_draft_receipt;
END $$;
