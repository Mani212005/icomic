-- A pending job the user cancels before its envelope is claimed is stopped for good: the
-- dispatcher never sends it and the debit is refunded in the same transaction. The outbox
-- records that outcome as its own state instead of calling the envelope delivered.
ALTER TABLE generation_dispatches
    DROP CONSTRAINT IF EXISTS generation_dispatches_state_check;

ALTER TABLE generation_dispatches
    ADD CONSTRAINT generation_dispatches_state_check
    CHECK (state IN ('pending', 'delivered', 'cancelled'));
