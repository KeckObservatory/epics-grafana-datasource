import { DataSourceInstanceSettings, SelectableValue } from '@grafana/data';
import { DataSourceWithBackend } from '@grafana/runtime';
import { EPICSDataSourceOptions, EPICSQuery } from './types';

export class DataSource extends DataSourceWithBackend<EPICSQuery, EPICSDataSourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<EPICSDataSourceOptions>) {
    super(instanceSettings);
  }

  async getSystems(): Promise<Array<SelectableValue<string>>> {
    return this.getResource('systems').then(({ systems }) =>
      systems ? Object.entries(systems).map(([value, label]) => ({ label, value } as SelectableValue<string>)) : []
    );
  }
}
