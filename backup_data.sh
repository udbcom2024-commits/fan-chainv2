#!/bin/bash
# FAN Node2 Data Backup Script for Linux
# Auto backup data directory every 30 seconds, keep last 100 backups

BACKUP_ROOT="/root/fan-chain/backups"
SOURCE_DIR="/root/fan-chain/data"
MAX_BACKUPS=100
INTERVAL=30

# Create backup root directory
mkdir -p "$BACKUP_ROOT"

do_backup() {
    # Generate timestamp
    TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
    BACKUP_DIR="$BACKUP_ROOT/backup_$TIMESTAMP"

    echo "========================================"
    echo "FAN Node2 Data Backup"
    echo "========================================"
    echo "Time: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "Source: $SOURCE_DIR"
    echo "Backup to: $BACKUP_DIR"

    # Check if source directory exists
    if [ ! -d "$SOURCE_DIR" ]; then
        echo "Warning: Source directory does not exist: $SOURCE_DIR"
        return 1
    fi

    # Get source directory size
    SOURCE_SIZE=$(du -sm "$SOURCE_DIR" | cut -f1)
    echo "Data size: ${SOURCE_SIZE} MB"

    # Execute backup
    echo "Backing up..."
    if cp -r "$SOURCE_DIR" "$BACKUP_DIR"; then
        echo "Backup successful!"

        # Record backup info
        INFO_FILE="$BACKUP_DIR/backup_info.txt"
        cat > "$INFO_FILE" << EOF
FAN Node2 Data Backup
=====================
Backup time: $(date '+%Y-%m-%d %H:%M:%S')
Source: $SOURCE_DIR
Data size: ${SOURCE_SIZE} MB
Backup type: Full backup

Contents:
- checkpoint_latest.dat
- state_latest.dat.gz
- block data
- account states
EOF

    else
        echo "Backup failed!"
        return 1
    fi

    # Clean old backups (keep last N backups)
    echo ""
    echo "Cleaning old backups..."
    BACKUP_COUNT=$(ls -1d "$BACKUP_ROOT"/backup_* 2>/dev/null | wc -l)
    if [ "$BACKUP_COUNT" -gt "$MAX_BACKUPS" ]; then
        DELETE_COUNT=$((BACKUP_COUNT - MAX_BACKUPS))
        ls -1dt "$BACKUP_ROOT"/backup_* | tail -n "$DELETE_COUNT" | xargs rm -rf
        echo "Deleted $DELETE_COUNT old backups"
    fi

    # Show backup count
    CURRENT_COUNT=$(ls -1d "$BACKUP_ROOT"/backup_* 2>/dev/null | wc -l)
    echo "Current backups: $CURRENT_COUNT / $MAX_BACKUPS"

    echo ""
    echo "Backup task completed!"
    echo "========================================"
}

# Main loop - backup every 30 seconds
echo "Starting continuous backup (every ${INTERVAL}s, keep last ${MAX_BACKUPS} backups)"
echo "Press Ctrl+C to stop"
echo ""

while true; do
    do_backup
    echo ""
    echo "Next backup in ${INTERVAL} seconds..."
    sleep $INTERVAL
done