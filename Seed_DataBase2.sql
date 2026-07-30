-- PostgreSQL delta seed for data added after Seed_DataBase.sql.
ALTER TABLE avatarconfigs ADD COLUMN IF NOT EXISTS effect text;
ALTER TABLE shopitems ADD COLUMN IF NOT EXISTS imagepath text;
ALTER TABLE shopitems ADD COLUMN IF NOT EXISTS slot text;
ALTER TABLE classrooms DROP COLUMN IF EXISTS studentids;

CREATE TABLE IF NOT EXISTS manualcoinadjustments (
    userid text PRIMARY KEY,
    amount integer NOT NULL
);

CREATE TABLE IF NOT EXISTS avatarbaseimages (
    id text PRIMARY KEY,
    label text NOT NULL,
    imagepath text NOT NULL
);

INSERT INTO users (userid, name, role, email, passwordhash, classroomid) VALUES
    ('21', 'Peter', 'student', 'peter@example.com', '$2a$10$JKh.VBpQzjvFdc9.SAvCUOm3/D95PqA/HjvQ3ixuwp7ySPspb4htO', 'classroom1')
ON CONFLICT (userid) DO UPDATE SET
    name = EXCLUDED.name,
    role = EXCLUDED.role,
    email = EXCLUDED.email,
    passwordhash = EXCLUDED.passwordhash,
    classroomid = EXCLUDED.classroomid;

INSERT INTO classroomstudents (classroomid, studentid) VALUES
    ('classroom1', '21')
ON CONFLICT (classroomid, studentid) DO NOTHING;

INSERT INTO shopitems (id, name, price, description, imagepath, slot) VALUES
    ('aura_sparkle', 'Sparkle Aura', 9, 'A bright aura for a student who keeps showing up.', '/static/images/cosmetics/aura_sparkle.png?v=2', 'effect'),
    ('brainrot', 'BrainRot', 10, 'Unlock BrainRot as a base avatar.', '/static/images/avatars/brainrot.png', 'base'),
    ('background_beach', 'Beach Background', 15, 'A soft shoreline with sand, sea, and gentle sky colors.', NULL, 'background'),
    ('background_forest', 'Forest Background', 15, 'A calm woodland palette with mossy greens and warm light.', NULL, 'background'),
    ('background_meadow', 'Meadow Background', 15, 'Pastel grass, tiny blooms, and a quiet afternoon feel.', NULL, 'background'),
    ('background_mountain', 'Mountain Background', 15, 'Cool ridge colors with misty lavender shadows.', NULL, 'background'),
    ('background_sky', 'Sky Background', 15, 'Airy blues and cloud-soft highlights for a clear day.', NULL, 'background'),
    ('background_sunset', 'Sunset Background', 15, 'Peach, rose, and gold tones for a mellow evening glow.', NULL, 'background'),
    ('cape_gold', 'Golden Cape', 12, 'A shiny cape for extra style.', '/static/images/cosmetics/cape_gold.png?v=2', 'clothing'),
    ('crown_flower', 'Flower Crown', 6, 'A leafy crown with bright classroom blooms.', '/static/images/cosmetics/crown_flower.png?v=2', 'hair_style'),
    ('d_money', 'D-Money', 10, 'Unlock D-Money as a base avatar.', '/static/images/avatars/d_money.png', 'base'),
    ('funk_rapper', 'Funk Rapper', 10, 'Unlock Funk Rapper as a base avatar.', '/static/images/avatars/funk_rapper.png', 'base'),
    ('glasses_rocket', 'Rocket Glasses', 10, 'A bold accessory for your avatar.', '/static/images/cosmetics/glasses_rocket.png?v=2', 'accessory'),
    ('hat_star', 'Star Hat', 5, 'A bright hat for a standout student.', '/static/images/cosmetics/hat_star.png?v=2', 'hair_style'),
    ('hat_wizard', 'Wizard Hat', 7, 'A tall purple hat for magical attendance streaks.', '/static/images/cosmetics/hat_wizard.png?v=2', 'hair_style'),
    ('headphones_gem', 'Gem Headphones', 9, 'Bright headphones with a gem on top.', '/static/images/cosmetics/headphones_gem.png?v=2', 'accessory'),
    ('hoodie_blue', 'Blue Hoodie', 7, 'A cozy hoodie for everyday questing.', '/static/images/cosmetics/hoodie_blue.png?v=2', 'clothing'),
    ('gopher', 'Gopher', 10, 'Unlock Gopher as a base avatar.', '/static/images/avatars/gopher.png', 'base'),
    ('mike', 'Mike', 10, 'Unlock Mike as a base avatar.', '/static/images/avatars/mike.png', 'base'),
    ('milkman', 'Milk Man', 10, 'Unlock Milk Man as a base avatar.', '/static/images/avatars/milkman.png', 'base'),
    ('peter', 'Peter', 10, 'Unlock Peter as a base avatar.', '/static/images/avatars/peter.png', 'base'),
    ('salaryman', 'Salary Man', 10, 'Unlock Salary Man as a base avatar.', '/static/images/avatars/salaryman.png', 'base'),
    ('scarf_red', 'Red Scarf', 6, 'A bold scarf for chilly morning check-ins.', '/static/images/cosmetics/scarf_red.png?v=2', 'clothing'),
    ('shades_pixel', 'Pixel Shades', 8, 'Blocky shades with old-school cool.', '/static/images/cosmetics/shades_pixel.png?v=2', 'accessory'),
    ('trail_comet', 'Comet Trail', 11, 'A comet streak that follows your avatar.', '/static/images/cosmetics/trail_comet.png?v=2', 'effect'),
    ('trail_rainbow', 'Rainbow Trail', 8, 'A colorful trail effect for your avatar.', '/static/images/cosmetics/trail_rainbow.png?v=2', 'effect')
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    price = EXCLUDED.price,
    description = EXCLUDED.description,
    imagepath = EXCLUDED.imagepath,
    slot = EXCLUDED.slot;

INSERT INTO avatarbaseimages (id, label, imagepath) VALUES
    ('brainrot', 'BrainRot', '/static/images/avatars/brainrot.png'),
    ('d_money', 'D-Money', '/static/images/avatars/d_money.png'),
    ('funk_rapper', 'Funk Rapper', '/static/images/avatars/funk_rapper.png'),
    ('gerald', 'Gerald', '/static/images/avatars/gerald.png'),
    ('gopher', 'Gopher', '/static/images/avatars/gopher.png'),
    ('mike', 'Mike', '/static/images/avatars/mike.png'),
    ('milkman', 'Milk Man', '/static/images/avatars/milkman.png'),
    ('peter', 'Peter', '/static/images/avatars/peter.png'),
    ('salaryman', 'Salary Man', '/static/images/avatars/salaryman.png')
ON CONFLICT (id) DO UPDATE SET
    label = EXCLUDED.label,
    imagepath = EXCLUDED.imagepath;

INSERT INTO ownedshopitems (userid, shopitemid) VALUES
    ('21', 'headphones_gem'),
    ('student1', 'glasses_rocket'),
    ('student1', 'background_beach'),
    ('student1', 'aura_sparkle')
ON CONFLICT (userid, shopitemid) DO NOTHING;

INSERT INTO avatarconfigs (userid, base, hairstyle, clothing, accessory, effect) VALUES
    ('21', 'peter', '', '', 'headphones_gem', ''),
    ('student1', 'funk_rapper', '', '', '', 'aura_sparkle')
ON CONFLICT (userid) DO UPDATE SET
    base = EXCLUDED.base,
    hairstyle = EXCLUDED.hairstyle,
    clothing = EXCLUDED.clothing,
    accessory = EXCLUDED.accessory,
    effect = EXCLUDED.effect;

INSERT INTO manualcoinadjustments (userid, amount) VALUES
    ('student1', 25)
ON CONFLICT (userid) DO UPDATE SET
    amount = EXCLUDED.amount;

WITH source (userid, amount, recordedat, description) AS (
    VALUES
        ('student1', 1, '2026-06-30T15:35:29-06:00'::timestamptz, 'Attendance reward for 2026-06-30'),
        ('student1', -10, '2026-06-30T15:35:32-06:00'::timestamptz, 'Purchased Rocket Glasses'),
        ('21', 1, '2026-06-30T16:23:12-06:00'::timestamptz, 'Attendance reward for 2026-06-30'),
        ('21', -9, '2026-06-30T16:23:21-06:00'::timestamptz, 'Purchased Gem Headphones'),
        ('student1', 1, '2026-07-01T16:08:04-06:00'::timestamptz, 'Attendance reward for 2026-07-01'),
        ('student1', -15, '2026-07-01T16:08:12-06:00'::timestamptz, 'Purchased Beach Background'),
        ('student1', 1, '2026-07-02T14:39:02-06:00'::timestamptz, 'Attendance reward for 2026-07-02'),
        ('student1', 1, '2026-07-02T09:07:28-06:00'::timestamptz, 'Attendance reward for 2026-07-02'),
        ('student1', -9, '2026-07-02T16:37:17-06:00'::timestamptz, 'Purchased Sparkle Aura'),
        ('student1', 1, '2026-07-08T17:33:20-06:00'::timestamptz, 'Attendance reward for 2026-07-08'),
        ('student1', 1, '2026-07-13T15:10:46-06:00'::timestamptz, 'Attendance reward for 2026-07-13')
)
INSERT INTO transactions (userid, amount, timestamp, description)
SELECT source.userid, source.amount, source.recordedat, source.description
FROM source
WHERE NOT EXISTS (
    SELECT 1
    FROM transactions AS target
    WHERE target.userid = source.userid
        AND target.amount = source.amount
        AND target.timestamp = source.recordedat
        AND target.description = source.description
);

INSERT INTO attendancerecords (userid, classroomid, presentdates, absentdates) VALUES
    ('student1', 'classroom1', '["2026-06-09","2026-06-10","2026-06-15","2026-06-22","2026-06-24","2026-06-30","2026-07-01","2026-07-02","2026-07-08","2026-07-13"]', NULL),
    ('21', 'classroom1', '["2026-06-30"]', '[]')
ON CONFLICT (userid, classroomid) DO UPDATE SET
    presentdates = EXCLUDED.presentdates,
    absentdates = EXCLUDED.absentdates;
