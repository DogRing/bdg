// GLSL for the curved-world hex renderer (WebGL1 + ANGLE_instanced_arrays).
// - tile: instanced hex prisms. Vertex shader bends the world down with view-space depth
//   (rolling/curved world), extrudes side walls only toward LOWER neighbours (no z-fight),
//   ripples water tops, and shades with one directional light. Fragment fogs into the sky.
// - sky: a full-screen vertical gradient (its horizon colour == the fog colour → seamless join).

export const TILE_VS = `
precision highp float;
attribute vec2 aLocal;      // unit hex-corner xz (centre = 0,0)
attribute float aTop;       // 1 = top ring, 0 = bottom ring of a wall
attribute float aFace;      // 0 = top face, 1 = side wall
attribute vec3 aNormal;
attribute float aEdge;      // side wall: which of the 6 edges (0..5)
attribute vec2 iCenter;     // per-instance hex centre (world x,z)
attribute float iType;      // per-instance tile type 0..4 (3 = water)
attribute float iElev;      // per-instance top elevation (world units)
attribute vec3 iNbrA;       // neighbour top elevations for edges 0,1,2
attribute vec3 iNbrB;       // neighbour top elevations for edges 3,4,5
uniform mat4 uProj, uView;
uniform float uCurv, uRelief, uCols, uTime, uHexR, uRipple;
uniform vec2 uCell;
uniform vec3 uLight;
varying vec2 vUV; varying float vDepth; varying float vLight; varying float vWater;
float nbr(float e){
  if(e<0.5) return iNbrA.x; else if(e<1.5) return iNbrA.y; else if(e<2.5) return iNbrA.z;
  else if(e<3.5) return iNbrB.x; else if(e<4.5) return iNbrB.y; return iNbrB.z;
}
void main(){
  vec2 hxz = iCenter + aLocal*uHexR;
  float bottomY = min(iElev, nbr(aEdge));                 // wall drops only to the lower neighbour
  float restRaw = (aFace<0.5 || aTop>0.5) ? iElev : bottomY;
  float atTop   = (aFace<0.5 || aTop>0.5) ? 1.0 : 0.0;
  float water   = (abs(iType-3.0)<0.5) ? 1.0 : 0.0;
  float y = restRaw*uRelief
          + water*atTop*uRelief*uRipple*sin(uTime*1.7 + hxz.x*0.8/uHexR + hxz.y*0.6/uHexR);
  vec4 vp = uView*vec4(hxz.x, y, hxz.y, 1.0);
  vp.y -= uCurv*(vp.z*vp.z);                               // rolling-world bend (view space)
  vDepth = -vp.z;
  gl_Position = uProj*vp;
  float col = mod(iType,uCols), row = floor(iType/uCols);
  vec2 luv = (aFace<0.5) ? (vec2(0.5)+aLocal*0.44) : vec2(0.5);   // top: hex uv · side: tile colour
  vUV = (vec2(col,row)+luv)*uCell;
  vLight = 0.42 + 0.58*max(dot(normalize(aNormal),uLight),0.0);
  vWater = water*((aFace<0.5)?1.0:0.0);
}`

export const TILE_FS = `
precision highp float;
uniform sampler2D uTex; uniform vec3 uFog; uniform float uFogNear, uFogFar, uTime;
varying vec2 vUV; varying float vDepth; varying float vLight; varying float vWater;
void main(){
  vec4 c = texture2D(uTex, vUV);
  if(c.a < 0.5) discard;
  vec3 col = c.rgb * vLight;
  col += vWater * 0.06 * vec3(0.55,0.8,1.0) * (0.5 + 0.5*sin(uTime*2.3 + vUV.x*90.0 + vUV.y*70.0));
  float f = smoothstep(uFogNear, uFogFar, vDepth);
  gl_FragColor = vec4(mix(col, uFog, f), 1.0);
}`

export const SKY_VS = `
precision highp float;
attribute vec2 aP; varying float vT;
void main(){ vT = aP.y*0.5+0.5; gl_Position = vec4(aP, 0.9999, 1.0); }`

export const SKY_FS = `
precision highp float;
uniform vec3 uZen, uHor; varying float vT;
void main(){ gl_FragColor = vec4(mix(uHor, uZen, clamp(vT,0.0,1.0)), 1.0); }`
