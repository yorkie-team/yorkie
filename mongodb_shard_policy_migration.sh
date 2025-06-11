#!/bin/bash

# MongoDB 샤딩 정책 변경 스크립트
# ranged → hashed 변경 (yorkie-meta DB)

set -e  # 에러 발생시 스크립트 중단

# 명령행 인수 처리
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        --host)
            MONGO_HOST="$2"
            shift 2
            ;;
        --user)
            MONGO_USER="$2"
            shift 2
            ;;
        --password)
            MONGO_PASSWORD="$2"
            shift 2
            ;;
        --db)
            DB_NAME="$2"
            shift 2
            ;;
        *)
            echo "알 수 없는 옵션: $1"
            usage
            exit 1
            ;;
    esac
done

# 설정 변수
MONGO_HOST="${MONGO_HOST:-localhost:27017}"
MONGO_USER="${MONGO_USER:-}"
MONGO_PASSWORD="${MONGO_PASSWORD:-}"
DB_NAME="${DB_NAME:-yorkie-meta}"
BACKUP_NAME="mongodb_backup_$(date +%Y%m%d_%H%M%S)"
BACKUP_DIR="./mongodb-dump"
LOG_FILE="migration_$(date +%Y%m%d_%H%M%S).log"

# 변경할 컬렉션 목록
ALL_COLLECTIONS=(\"changes\",\"snapshots\",\"versionvectors\",\"clients\",\"documents\",\"projects\",\"users\")
DOC_ID_HASHED_COLLECTIONS=(\"changes\",\"snapshots\",\"versionvectors\")
PROJECT_ID_RANGED_COLLECTIONS=(\"clients\",\"documents\")

# 로그 함수
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# 로그 및 실행 헬퍼
log_and_run() {
    local log_message="$1"
    local command_to_run="$2"
    
    log "$log_message"
    
    # 명령 실행 및 결과 로깅
    local output
    output=$(eval "$command_to_run" 2>&1)
    
    if [ $? -ne 0 ]; then
        log "ERROR: Command failed: $command_to_run"
        log "Output: $output"
        exit 1
    fi
    
    # 성공 시 출력 로깅
    echo "$output" | while IFS= read -r line; do
        log "  $line"
    done
}

# MongoDB 연결 문자열 생성
get_mongo_uri() {
    if [[ -n "$MONGO_USER" && -n "$MONGO_PASSWORD" ]]; then
        echo "mongodb://${MONGO_USER}:${MONGO_PASSWORD}@${MONGO_HOST}"
    else
        echo "mongodb://${MONGO_HOST}"
    fi
}

# MongoDB 연결 옵션 생성
get_mongosh_options() {
    local options="--host $MONGO_HOST"
    if [[ -n "$MONGO_USER" && -n "$MONGO_PASSWORD" ]]; then
        options="$options --username $MONGO_USER --password $MONGO_PASSWORD --authenticationDatabase admin"
    fi
    echo "$options"
}

# MongoDB 연결 확인
check_mongo_connection() {
    log "MongoDB 연결 확인 중..."
    local mongosh_opts=$(get_mongosh_options)
    if ! mongosh $mongosh_opts --eval "db.adminCommand('ping')" >/dev/null 2>&1; then
        log "ERROR: MongoDB에 연결할 수 없습니다."
        exit 1
    fi
    log "MongoDB 연결 성공"
}


# 전체 DB 백업
backup_full_database() {
    log "전체 DB 백업 시작..."
    mkdir -p "$BACKUP_DIR/$BACKUP_NAME"
    
    MONGO_URI=$(get_mongo_uri)
    
    log "백업 중: $DB_NAME (전체 데이터베이스)"
    if ! mongodump --uri="$MONGO_URI" --db="$DB_NAME" --out="$BACKUP_DIR/$BACKUP_NAME"; then
        log "ERROR: 전체 DB 백업 실패"
        exit 1
    fi
    
    log "전체 DB 백업 완료: $BACKUP_DIR/$BACKUP_NAME"
}

# 메인 DB에서 컬렉션 삭제
drop_collections() {
    log "메인 DB에서 컬렉션 삭제 시작..."
    local mongosh_opts=$(get_mongosh_options)
    
    mongosh $mongosh_opts --eval "
        db = db.getSiblingDB('$DB_NAME');
        
        print('=== DB 컬렉션 삭제 시작 ===');
        
        [${ALL_COLLECTIONS[*]}].forEach(function(collName) {
            print('삭제 중: ' + collName);
            
            try {
                var dropResult = db.runCommand({drop: collName});
                if (dropResult.ok === 1) {
                    print('  - ' + collName + ' 삭제 성공');
                } else {
                    print('  - ' + collName + ' 삭제 실패: ' + (dropResult.errmsg || 'Unknown'));
                }
            } catch(e) {
                print('  - ' + collName + ' 삭제 오류: ' + e.message);
            }
        });
        
        
        print('=== DB 컬렉션 삭제 완료 ===');
    " 2>&1 | tee -a "$LOG_FILE"
}

# 인덱스 복원
restore_indexes() {
    log "인덱스 복원 시작..."
    local mongosh_opts=$(get_mongosh_options)
    
    MONGO_URI=$(get_mongo_uri)
    
    # BSON 파일을 제외하고 메타데이터 파일들만 복원 (인덱스 정보)
    log "인덱스 복원 중: $DB_NAME"
    
    # 대상 디렉토리 생성
    mkdir -p "$BACKUP_DIR/only-indexes/$BACKUP_NAME/$DB_NAME"
    
    rsync -au --recursive --include="*.json" --exclude='*.bson' "$BACKUP_DIR/$BACKUP_NAME/$DB_NAME/" "$BACKUP_DIR/only-indexes/$BACKUP_NAME/$DB_NAME"

    # JSON 파일만 있으므로 인덱스 정보만 복원됨
    if ! mongorestore --uri="$MONGO_URI" --db="$DB_NAME" "$BACKUP_DIR/only-indexes/$BACKUP_NAME/$DB_NAME"; then
        log "ERROR: 인덱스 복원 실패"
        exit 1
    fi
    
    log "인덱스 복원 완료"
}

# 새 샤딩 정책 설정
setup_new_sharding_policy() {
    log "새 샤딩 정책(hashed) 설정 시작..."
    local mongosh_opts=$(get_mongosh_options)
    
    mongosh $mongosh_opts --eval "
        db = db.getSiblingDB('$DB_NAME');
        
        print('=== 새 샤딩 정책 설정 시작 ===');
        
        // 샤딩 활성화
        sh.enableSharding('$DB_NAME');
        print('샤딩 활성화 완료');
        
        [${DOC_ID_HASHED_COLLECTIONS[*]}].forEach(function(collName) {
            print('샤딩 설정 중: ' + collName);
            
            try {
                // 컬렉션 생성
                db.createCollection(collName);
                print('  - 컬렉션 생성 완료');
                
                // hashed 샤드키로 샤딩 설정
                sh.shardCollection('$DB_NAME.' + collName, {'doc_id': 'hashed'});
                print('  - hashed 샤딩 설정 완료');
            } catch(e) {
                print('  - ' + collName + ' 샤딩 설정 오류: ' + e.message);
            }
        });

        [${PROJECT_ID_RANGED_COLLECTIONS[*]}].forEach(function(collName) {
            print('샤딩 설정 중: ' + collName);
            
            try {
                // 컬렉션 생성
                db.createCollection(collName);
                print('  - 컬렉션 생성 완료');

                // ranged 샤드키로 샤딩 설정
                sh.shardCollection('$DB_NAME.' + collName, {'project_id': 1});
                print('  - ranged 샤딩 설정 완료');
            } catch(e) {
                print('  - ' + collName + ' 샤딩 설정 오류: ' + e.message);
            }
        });
        
        print('=== 새 샤딩 정책 설정 완료 ===');
    " 2>&1 | tee -a "$LOG_FILE"
}

# 전체 DB 복원
restore_full_database() {
    log "전체 DB 복원 시작..."
    
    MONGO_URI=$(get_mongo_uri)
    
    log "복원 중: $DB_NAME (전체 데이터베이스)"
    if ! mongorestore --uri="$MONGO_URI" --db="$DB_NAME" "$BACKUP_DIR/$BACKUP_NAME/$DB_NAME"; then
        log "ERROR: 전체 DB 복원 실패"
        exit 1
    fi
    
    log "전체 DB 복원 완료"
}

# 마이그레이션 검증
verify_migration() {
    log "마이그레이션 검증 시작..."
    
    for collection in "changes" "snapshots" "versionvectors" "clients" "documents" "projects" "users"; do
        log "검증 중: $DB_NAME.$collection"
        local mongosh_opts=$(get_mongosh_options)
        
        mongosh $mongosh_opts --eval "
            db = db.getSiblingDB('$DB_NAME');
            
            // 문서 수 확인
            var count = db.$collection.countDocuments();
            print('문서 수: ' + count);
            
            // 샤딩 정보 확인
            var shardInfo = db.runCommand({collStats: '$collection'});
            if (shardInfo.sharded) {
                print('샤딩 상태: 활성화');
            } else {
                print('샤딩 상태: 비활성화');
            }
            print('---');
        " 2>&1 | tee -a "$LOG_FILE"
    done
}

# 메인 실행 함수
main() {
    log "MongoDB 샤딩 정책 마이그레이션 시작"
    log "대상 DB: $DB_NAME"
    log "대상 컬렉션: ${COLLECTIONS[*]}"
    log "변경: ranged → hashed"
    
    # 사용자 확인
    echo "백업 디렉토리: $BACKUP_DIR/$BACKUP_NAME"
    
    # 실행 단계
    mkdir -p "$BACKUP_DIR"
    # check_mongo_connection
    backup_full_database
    # drop_collections
    # restore_indexes
    # setup_new_sharding_policy
    # restore_full_database
    # verify_migration
    
    log "마이그레이션 완료"
    log "백업 파일: $BACKUP_DIR/$BACKUP_NAME (안전 확인 후 삭제 가능)"
    log "로그 파일: $LOG_FILE"
}

# 스크립트 실행
main "$@" 