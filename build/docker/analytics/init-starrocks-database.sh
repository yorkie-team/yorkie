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
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: channel_events' || echo -e 'channel_events table not found'
mysql -h starrocks-fe -P 9030 -u root -e 'show tables from yorkie\G' | grep 'Tables_in_yorkie: session_events' || echo -e 'session_events table not found'


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

# variable
routine_loads=(yorkie.events yorkie.channel_events yorkie.session_events)
for routine_load in ${routine_loads[@]}; do
  state=$(mysql -h starrocks-fe -P 9030 -u root -e "show routine load for $routine_load\G" 2>/dev/null | grep State: | sed 's/.*State: //')
  echo "Routine load $routine_load state: $state"
  if [ "$state" = "PAUSED" ]; then
    echo "Resuming routine load: $routine_load"
    mysql -h starrocks-fe -P 9030 -u root -e "RESUME ROUTINE LOAD FOR $routine_load;"
  fi
done

echo -e 'Final routine load status:'
mysql -h starrocks-fe -P 9030 -u root -e 'show routine load from yorkie\G'

