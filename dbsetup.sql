-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";


-- TODO: Add admin user manually
-- Then set is_admin to true
UPDATE users 
SET is_admin = TRUE 
WHERE username = 'tetr4';