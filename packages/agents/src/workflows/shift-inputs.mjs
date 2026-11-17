import { shiftInputs } from './check-inputs.mjs';
export { tableRows, shiftInputs } from './check-inputs.mjs';
import {checkQualityInputs} from './data-quality.mjs';
export async function checkShiftInputs(inputs){
  const parsed=shiftInputs(inputs);
  const columns=inputs.keyColumns;
  for(const dataset of [inputs.baselineData,inputs.currentData])await checkQualityInputs({dataset,keyColumns:columns});
  return parsed;
}
