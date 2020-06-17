import { DataSourceInstanceSettings } from '@grafana/data';
import { DataSourceWithBackend } from '@grafana/runtime';
import { EPICSDataSourceOptions, EPICSQuery } from './types';

export class DataSource extends DataSourceWithBackend<EPICSQuery, EPICSDataSourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<EPICSDataSourceOptions>) {
    super(instanceSettings);
  }
}
