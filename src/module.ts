import { DataSourcePlugin } from '@grafana/data';
import { DataSource } from './DataSource';
import { ConfigEditor } from './ConfigEditor';
import { QueryEditor } from './QueryEditor';
import { EPICSQuery, EPICSDataSourceOptions } from './types';

export const plugin = new DataSourcePlugin<DataSource, EPICSQuery, EPICSDataSourceOptions>(DataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
