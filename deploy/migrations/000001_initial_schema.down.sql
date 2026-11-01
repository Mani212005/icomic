-- Revert initial schema migration

DROP TABLE IF EXISTS exports;
DROP TABLE IF EXISTS credit_ledger;
DROP TABLE IF EXISTS stage_approvals;
DROP TABLE IF EXISTS colour_regions;
DROP TABLE IF EXISTS colour_palettes;
DROP TABLE IF EXISTS pages;
DROP TABLE IF EXISTS panels;
DROP TABLE IF EXISTS characters;
DROP TABLE IF EXISTS generation_jobs;
DROP TABLE IF EXISTS chapters;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS plans;
