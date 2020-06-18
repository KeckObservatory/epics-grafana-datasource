import defaults from 'lodash/defaults';

import React, { ChangeEvent, PureComponent } from 'react';
import { InlineFormLabel, SegmentAsync } from '@grafana/ui';
import { QueryEditorProps } from '@grafana/data';
import { DataSource } from './DataSource';
import { defaultQuery, EPICSDataSourceOptions, EPICSQuery } from './types';

type Props = QueryEditorProps<DataSource, EPICSQuery, EPICSDataSourceOptions>;

export class QueryEditor extends PureComponent<Props> {
  onSystemChange = (item: any) => {
    const { onChange, query } = this.props;
    // Repopulate the keyword list based on the service selected
    onChange({ ...query, system: item.value });
  };

  onQueryTextChange = (event: ChangeEvent<HTMLInputElement>) => {
    const { onChange, query } = this.props;
    onChange({ ...query, queryText: event.target.value });
  };

  render() {
    const datasource = this.props.datasource;
    const query = defaults(this.props.query, defaultQuery);
    //const { queryText } = query;

    // noinspection CheckTagEmptyBody
    return (
      <>
        <div className="gf-form-inline">
          <InlineFormLabel
            width={10}
            className="query-system"
            tooltip={
              <p>
                Select a <code>system</code>.
              </p>
            }
          >
            System selection
          </InlineFormLabel>
          <SegmentAsync
            loadOptions={() => datasource.getSystems()}
            placeholder="dcs1"
            value={query.system}
            allowCustomValue={false}
            onChange={this.onSystemChange}
          ></SegmentAsync>
        </div>
      </>
    );
  }
}
