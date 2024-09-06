# Grafana Setup Guide

## Importing Dashboards

To import dashboards into Grafana, follow these steps:

1. Open Grafana in your browser (http://localhost:3000).
2. Log in with the default credentials (admin/admin).
3. Click "Dashboards" in the left sidebar.
4. Click "New" and then "Import".
5. Use the Yorkie dashboard ID `18560` to import the dashboard.
6. Click "Load" and "Import" and the dashboard will be made.

## Setting a Default Dashboard

If you want to set a default dashboard, follow these steps:

1. Open the [Yorkie dashboard](https://grafana.com/grafana/dashboards/18560).
2. Download the dashboard JSON file.
3. Paste the contents into `build/docker/grafana/yorkie_dashboard.json`.
4. Mount the docker volume to the Grafana container by adding the following line to your `docker-compose.yml` file:

   ```yaml
   volumes:
     - ./grafana/your_dashboard_name.json:/var/lib/grafana/your_dashboard_name.json:ro
   ```

You can import other dashboards by following the same steps.