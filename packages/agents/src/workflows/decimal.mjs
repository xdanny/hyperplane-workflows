const require=(ok,message)=>{if(!ok)throw new Error(message);};
// Fixed decimal arithmetic for reports: up to 24 fractional digits, no binary
// rounding allowance that could hide a small residual in a large money total.
const scale=10n**24n;
export function amount(value){
 require(typeof value==='string'||typeof value==='number'&&Number.isFinite(value)&&(!Number.isInteger(value)||Number.isSafeInteger(value)),'Use a decimal string for amounts beyond safe numeric precision');
 const s=String(value);require(s.length<=96&&/^[+-]?\d+(?:\.\d+)?$/.test(s),'Use a plain decimal amount (no exponent)');
 const [whole,fraction='']=s.replace(/^[+-]/,'').split('.');require(fraction.length<=24,'Amounts support up to 24 decimal places');
 return (s.startsWith('-')?-1n:1n)*(BigInt(whole)*scale+BigInt(fraction.padEnd(24,'0')));
}
const abs=n=>n<0n?-n:n;
const units=['percent_of_a','percent_of_b','absolute','count','exact'];
export function withinTolerance(unexplained,totalA,totalB,tolerance){
 const value=amount(tolerance.value??0);require(value>=0n&&units.includes(tolerance.unit),'Use a nonnegative tolerance and a supported unit');
 const delta=abs(amount(unexplained));
 if(tolerance.unit==='exact'){require(value===0n,'Exact tolerance must be zero');return delta===0n;}
 if(tolerance.unit==='percent_of_a'||tolerance.unit==='percent_of_b')return delta*100n*scale<=abs(amount(tolerance.unit==='percent_of_a'?totalA:totalB))*value;
 return delta<=value;
}
