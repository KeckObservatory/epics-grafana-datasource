import { DataQuery, DataSourceJsonData } from '@grafana/data';

export interface EPICSQuery extends DataQuery {
  queryText: string;
  unitConversion: number;
  transform: number;
}

export const defaultQuery: Partial<EPICSQuery> = {
  unitConversion: 0,
  transform: 0,
};

/**
 * These are options configured for each DataSource instance
 */
export interface EPICSDataSourceOptions extends DataSourceJsonData {
  server: string;
  managePort: string;
  dataPort: string;
}
