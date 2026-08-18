#!/bin/bash
set -e

sleep 5s
echo -e 'Checking Starrocks status'
mysql -h starrocks-fe -P 9030 -u root -e 'show frontends\G' | grep 'Alive: true' || echo -e 'Frontend is not ready'
mysql -h starrocks-fe -P 9030 -u root -e 'show backends\G' | grep 'Alive: true' || echo -e 'Backend is not ready'


echo -e 'Creating Yorkie database and tables'
if mysql -h starrocks-fe -P 9030 -u root < /init-create-table.sql; then
  echo -e 'Successfully created database and tables'
else
  echo -e 'Tables may already exist, continuing...'
fi

echo -e 'Checking Yorkie database'
mysql -h starrocks-fe -P 9030 -u root -e 'show databases\G'
mysql -h starrocks-fe -P 9030 -u root -e 'show databases\G' | grep 'Database: yorkie' || echo -e 'Yorkie database not found'

echo -e 'Checking tables'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: user_events' || echo -e 'user_events table not found'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: document_events' || echo -e 'document_events table not found'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: channel_events' || echo -e 'channel_events table not found'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: session_events' || echo -e 'session_events table not found'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: client_events' || echo -e 'client_events table not found'


echo -e 'Creating materialized views'
# A synchronous materialized view has no working IF NOT EXISTS guard: the clause
# parses but still errors once the view exists. --force is what keeps a re-run
# of this script from failing.
mysql -h starrocks-fe -P 9030 -u root --force < /init-create-mv.sql \
  || echo -e 'Could not run the materialized view script, continuing...'

echo -e 'Checking materialized views'
# The rollup build runs asynchronously and the index only shows up in `desc ...
# all` once it finishes, so poll rather than checking straight after the CREATE.
# A view that never shows up is not fatal: the queries fall back to scanning the
# base table, which is slower but still correct.
mvs=(user_events:mv_user_hll_daily document_events:mv_document_hll_daily channel_events:mv_channel_hll_daily session_events:mv_session_hll_daily_ch client_events:mv_client_hll_daily)
for mv in "${mvs[@]}"; do
  table=${mv%%:*}
  index=${mv##*:}
  attempt=0
  until mysql -h starrocks-fe -P 9030 -u root -e "desc yorkie.$table all\G" 2>/dev/null | grep -q "IndexName: $index"; do
    attempt=$((attempt + 1))
    if [ $attempt -ge 60 ]; then
      echo -e "$index is not ready on $table yet; it may still be building"
      break
    fi
    sleep 2s
  done
  [ $attempt -lt 60 ] && echo -e "$index is ready on $table"
done

sleep 5s
echo -e 'Creating routine load'
if mysql -h starrocks-fe -P 9030 -u root < /init-create-routine-load.sql 2>/dev/null; then
  echo -e 'Successfully created routine loads'
else
  echo -e 'Routine loads may already exist, continuing...'
fi

sleep 10s
echo -e 'Checking and resuming routine loads if needed'
mysql -h starrocks-fe -P 9030 -u root -e 'show routine load from yorkie\G'

routine_loads=(yorkie.user_events yorkie.document_events yorkie.channel_events yorkie.session_events yorkie.client_events)
for routine_load in "${routine_loads[@]}"; do
  state=$(mysql -h starrocks-fe -P 9030 -u root -e "show routine load for $routine_load\G" 2>/dev/null | grep State: | sed 's/.*State: //')
  echo "Routine load $routine_load state: $state"
  if [ "$state" = "PAUSED" ]; then
    echo "Resuming routine load: $routine_load"
    mysql -h starrocks-fe -P 9030 -u root -e "RESUME ROUTINE LOAD FOR $routine_load;"
  fi
done

echo -e 'Final routine load status:'
mysql -h starrocks-fe -P 9030 -u root -e 'show routine load from yorkie\G'
