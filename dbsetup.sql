-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create users table with GORM-compatible structure
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_admin BOOLEAN NOT NULL DEFAULT FALSE
);

-- Insert admin user if not exists
INSERT INTO users (username, email, password, created_at, is_admin)
SELECT 'admin', 'admin@example.com', 'admin', CURRENT_TIMESTAMP, TRUE
WHERE NOT EXISTS (
    SELECT 1 FROM users WHERE username = 'admin'
);
