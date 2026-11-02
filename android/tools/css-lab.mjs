// CSS Color 4 D50 Lab mixing, used by Pierre's color-mix(in lab, ...) backgrounds.
const multiply=(matrix,vector)=>matrix.map(row=>row.reduce((sum,n,i)=>sum+n*vector[i],0));
const rgbToXyz=[[.4123907992659595,.357584339383878,.1804807884018343],[.2126390058715104,.715168678767756,.0721923153607337],[.0193308187155919,.119194779794626,.9505321522496607]];
const xyzToRgb=[[3.2409699419045226,-1.537383177570094,-.4986107602930034],[-.9692436362808796,1.8759675015077202,.0415550574071756],[.0556300796969937,-.2039769588889765,1.0569715142428786]];
const toD50=[[1.0479298208405488,.0229467933410191,-.0501922295431356],[.0296278156881593,.990434484573249,-.0170738250293851],[-.0092430581525912,.0150551448965779,.7518742814281371]];
const toD65=[[.9554734527042182,-.0230985368742614,.0632593086610217],[-.0283697069632081,1.0099954580058226,.021041398966943],[.0123140016883199,-.0205076964334779,1.3303659366080753]];
const white=[.3457/.3585,1,(1-.3457-.3585)/.3585],epsilon=216/24389,kappa=24389/27;
function lab(hex) {
 if(/^#[\da-f]{3}$/i.test(hex))hex='#'+[...hex.slice(1)].map(c=>c+c).join('');
 if(!/^#[\da-f]{6}(?:[\da-f]{2})?$/i.test(hex))throw new Error(`Invalid CSS hex color: ${hex}`);
 const rgb=hex.slice(1).match(/../g).slice(0,3).map(n=>parseInt(n,16)/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);
 const f=multiply(toD50,multiply(rgbToXyz,rgb)).map((v,i)=>v/white[i]).map(v=>v>epsilon?Math.cbrt(v):(kappa*v+16)/116);
 return [116*f[1]-16,500*(f[0]-f[1]),200*(f[1]-f[2])];
}
function hex(lab) {
 const y=(lab[0]+16)/116,f=[y+lab[1]/500,y,y-lab[2]/200];
 const xyz=f.map(v=>v**3>epsilon?v**3:(116*v-16)/kappa).map((v,i)=>v*white[i]);
 return '#'+multiply(xyzToRgb,multiply(toD65,xyz)).map(v=>v<=.0031308?12.92*v:1.055*v**(1/2.4)-.055).map(v=>Math.round(Math.max(0,Math.min(1,v))*255).toString(16).padStart(2,'0')).join('');
}
export function labMix(first,second,weight) {
 const a=lab(first),b=lab(second);
 return hex(a.map((v,i)=>v*weight+b[i]*(1-weight)));
}
