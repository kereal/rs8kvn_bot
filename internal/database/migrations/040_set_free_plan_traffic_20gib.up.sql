-- Free plan quota: 50 GiB -> 20 GiB (21474836480 bytes).
UPDATE plans SET traffic_limit = 21474836480 WHERE name = 'free';
