UPDATE generation_dispatches SET state = 'delivered' WHERE state = 'cancelled';

ALTER TABLE generation_dispatches
    DROP CONSTRAINT IF EXISTS generation_dispatches_state_check;

ALTER TABLE generation_dispatches
    ADD CONSTRAINT generation_dispatches_state_check
    CHECK (state IN ('pending', 'delivered'));
