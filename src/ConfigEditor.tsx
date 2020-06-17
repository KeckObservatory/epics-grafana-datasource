import React, { ChangeEvent, PureComponent } from 'react';
import { LegacyForms } from '@grafana/ui';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { EPICSDataSourceOptions } from './types';

const { FormField } = LegacyForms;

interface Props extends DataSourcePluginOptionsEditorProps<EPICSDataSourceOptions> {}

interface State {}

export class ConfigEditor extends PureComponent<Props, State> {
  onServerChange = (event: ChangeEvent<HTMLInputElement>) => {
    const { onOptionsChange, options } = this.props;
    const jsonData = {
      ...options.jsonData,
      server: event.target.value,
    };
    onOptionsChange({ ...options, jsonData });
  };

  onManagePortChange = (event: ChangeEvent<HTMLInputElement>) => {
    const { onOptionsChange, options } = this.props;
    const jsonData = {
      ...options.jsonData,
      managePort: event.target.value,
    };
    onOptionsChange({ ...options, jsonData });
  };

  onDataPortChange = (event: ChangeEvent<HTMLInputElement>) => {
    const { onOptionsChange, options } = this.props;
    const jsonData = {
      ...options.jsonData,
      dataPort: event.target.value,
    };
    onOptionsChange({ ...options, jsonData });
  };

  render() {
    const { options } = this.props;
    const { jsonData } = options;

    return (
      <div className="gf-form-group">
        <div className="gf-form">
          <FormField
            label="EPICS Channel archive server"
            labelWidth={13}
            inputWidth={20}
            onChange={this.onServerChange}
            value={jsonData.server || ''}
            placeholder="k1dataserver"
          />
        </div>
        <div className="gf-form">
          <FormField
            label="Management port"
            labelWidth={13}
            inputWidth={4}
            onChange={this.onManagePortChange}
            value={jsonData.managePort || ''}
            placeholder="17665"
          />
        </div>
        <div className="gf-form">
          <FormField
            label="Data port"
            labelWidth={13}
            inputWidth={4}
            onChange={this.onDataPortChange}
            value={jsonData.dataPort || ''}
            placeholder="17668"
          />
        </div>
      </div>
    );
  }
}
