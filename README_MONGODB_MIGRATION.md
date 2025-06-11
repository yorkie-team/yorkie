# MongoDB 샤딩 정책 마이그레이션 가이드

MongoDB의 yorkie-meta 데이터베이스 샤딩 정책을 ranged에서 hashed로 변경하는 스크립트입니다.

## 개요

다음 컬렉션의 샤딩 정책을 변경합니다:
- `yorkie-meta.changes`: doc_id 기준 ranged → hashed
- `yorkie-meta.snapshots`: doc_id 기준 ranged → hashed  
- `yorkie-meta.versionvertors`: doc_id 기준 ranged → hashed

## 사전 요구사항

- MongoDB 도구 설치 (`mongosh`, `mongodump`, `mongorestore`)
- MongoDB 샤드 클러스터 접근 권한
- 충분한 디스크 공간 (백업용)

## 환경 변수 설정

```bash
export MONGO_HOST="your-mongo-host:27017"
export MONGO_USER="your-username"          # 선택사항
export MONGO_PASSWORD="your-password"      # 선택사항
```

## 사용법

### 1. 스크립트 실행 권한 부여
```bash
chmod +x mongodb_shard_policy_migration.sh
```

### 2. 스크립트 실행
```bash
./mongodb_shard_policy_migration.sh
```

## 실행 과정

1. **연결 확인**: MongoDB 연결 상태 검증
2. **현재 상태 확인**: 기존 샤딩 정책 및 데이터 확인
3. **데이터 백업**: 각 컬렉션을 별도 디렉토리에 백업
4. **컬렉션 삭제**: 기존 샤딩된 컬렉션 제거
5. **새 컬렉션 생성**: hashed 샤딩 정책으로 재생성
6. **데이터 복원**: 백업된 데이터를 새 컬렉션에 복원
7. **검증**: 마이그레이션 결과 확인

## 주의사항

⚠️ **중요**: 이 스크립트는 운영 환경에서 서비스 중단을 야기합니다.

- 마이그레이션 중에는 해당 컬렉션에 접근할 수 없습니다
- 반드시 서비스 점검 시간에 실행하세요
- 백업이 자동으로 생성되지만, 별도 백업을 추천합니다

## 생성되는 파일들

- `mongodb_backup_YYYYMMDD_HHMMSS/`: 백업 디렉토리
- `migration_YYYYMMDD_HHMMSS.log`: 마이그레이션 로그

## 트러블슈팅

### 연결 실패
```bash
# MongoDB 서비스 상태 확인
mongosh --host $MONGO_HOST --eval "db.adminCommand('ping')"
```

### 권한 오류
- 샤드 관리 권한이 있는 사용자로 실행하세요
- 필요한 권한: `clusterAdmin`, `dbAdmin`

### 백업/복원 실패
- 디스크 공간 확인
- MongoDB 도구 버전 호환성 확인

## 롤백 방법

마이그레이션 실패 시 수동 롤백:

```bash
# 1. 실패한 컬렉션 삭제
mongosh --host $MONGO_HOST
use yorkie-meta
db.changes.drop()
db.snapshots.drop()
db.versionvertors.drop()

# 2. 백업에서 복원
mongorestore --host $MONGO_HOST --db yorkie-meta ./mongodb_backup_YYYYMMDD_HHMMSS/yorkie-meta/
```

## 성능 고려사항

- **hashed 샤딩 장점**: 균등한 데이터 분산, 더 나은 쓰기 성능
- **ranged 샤딩 대비**: 범위 쿼리 성능은 다소 감소할 수 있음
- **인덱스**: doc_id hashed 인덱스가 자동 생성됨

## 마이그레이션 완료 후 확인사항

1. 각 컬렉션의 문서 수가 동일한지 확인
2. 샤딩 상태 및 정책 확인
3. 애플리케이션 연결 테스트
4. 성능 모니터링

## 예시 실행 결과

```
[2024-01-15 10:30:00] MongoDB 샤딩 정책 마이그레이션 시작
[2024-01-15 10:30:01] MongoDB 연결 성공
[2024-01-15 10:30:02] 데이터 백업 시작...
[2024-01-15 10:32:15] 백업 완료: ./mongodb_backup_20240115_103000
[2024-01-15 10:32:16] 기존 샤딩된 컬렉션 삭제 시작...
[2024-01-15 10:33:00] 새 샤딩 정책(hashed)으로 컬렉션 생성 시작...
[2024-01-15 10:33:30] 데이터 복원 시작...
[2024-01-15 10:35:45] 데이터 복원 완료
[2024-01-15 10:35:46] 마이그레이션 검증 시작...
[2024-01-15 10:35:50] 마이그레이션 완료!
``` 