-- Additive owner scoping for optional encrypted user-provider connections.
-- Apply after Seed_DataBase3.sql before enabling user-owned provider linking.
-- This file is not run by the application.
BEGIN;

ALTER TABLE integrationconnections
    ADD COLUMN IF NOT EXISTS owneruserid text;

DO $$ BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'fk_integrationconnections_owner_user'
    ) THEN
        ALTER TABLE integrationconnections
            ADD CONSTRAINT fk_integrationconnections_owner_user
            FOREIGN KEY (owneruserid) REFERENCES users(userid);
    END IF;
END $$;

DO $$
DECLARE
    existing_constraint text;
BEGIN
    SELECT c.conname
    INTO existing_constraint
    FROM pg_constraint c
    JOIN pg_class t ON t.oid = c.conrelid
    WHERE t.relname = 'integrationconnections'
      AND c.contype = 'c'
      AND pg_get_constraintdef(c.oid) ILIKE '%connectionrole%'
    LIMIT 1;

    IF existing_constraint IS NOT NULL THEN
        EXECUTE format(
            'ALTER TABLE integrationconnections DROP CONSTRAINT %I',
            existing_constraint
        );
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'ck_integrationconnections_role'
    ) THEN
        ALTER TABLE integrationconnections
            ADD CONSTRAINT ck_integrationconnections_role
            CHECK (connectionrole IN (
                'roster_source',
                'attendance_destination',
                'student_link'
            ));
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_integrationconnections_student_provider
    ON integrationconnections (owneruserid, providerkind)
    WHERE connectionrole = 'student_link';

CREATE INDEX IF NOT EXISTS ix_integrationconnections_owner
    ON integrationconnections (owneruserid)
    WHERE owneruserid IS NOT NULL;

COMMIT;
