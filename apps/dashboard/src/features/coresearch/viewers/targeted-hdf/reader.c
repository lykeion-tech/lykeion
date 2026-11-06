#include <hdf5.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <limits.h>
#define MAX_ITEMS 64
#define MAX_VALUES 200
#define MAX_RANK 4

typedef struct {char name[256]; char type[24]; unsigned rank, shape[4], count; char values[MAX_VALUES][80];} item;
static item items[MAX_ITEMS];static unsigned count, visited;static int partial,bad,matlab;
static int attr_text(hid_t object,const char *name,char *out,size_t cap){
 if(H5Aexists(object,name)<=0)return 0; hid_t a=H5Aopen(object,name,H5P_DEFAULT);if(a<0)return -1;
 hid_t t=H5Aget_type(a),s=H5Aget_space(a);int ok=0;
 if(t>=0&&s>=0&&H5Tget_class(t)==H5T_STRING&&H5Tis_variable_str(t)==0&&H5Sget_simple_extent_npoints(s)==1){size_t n=H5Tget_size(t);if(n>0&&n<cap){memset(out,0,cap);if(H5Aread(a,t,out)>=0){out[n]=0;ok=1;}}}
 if(s>=0)H5Sclose(s);if(t>=0)H5Tclose(t);H5Aclose(a);return ok?1:-1;
}
/* MATLAB complex numbers use a two-field compound named real/imag.
   Reject arbitrary compound, padded, overlapping, nested or non-IEEE layouts. */
static int matlab_complex(hid_t type, size_t *component) {
 if(H5Tget_nmembers(type)!=2)return 0;
 hid_t a=H5Tget_member_type(type,0),b=H5Tget_member_type(type,1);
 char *an=H5Tget_member_name(type,0),*bn=H5Tget_member_name(type,1);
 int valid=0;
 if(a>=0&&b>=0&&an&&bn&&H5Tequal(a,b)>0&&
    ((!strcmp(an,"real")&&!strcmp(bn,"imag"))||(!strcmp(an,"imag")&&!strcmp(bn,"real")))) {
  size_t n=H5Tget_size(a),total=H5Tget_size(type),o0=H5Tget_member_offset(type,0),o1=H5Tget_member_offset(type,1);
  if((n==4||n==8)&&total==2*n&&((o0==0&&o1==n)||(o1==0&&o0==n))) {
   hid_t le=n==4?H5T_IEEE_F32LE:H5T_IEEE_F64LE,be=n==4?H5T_IEEE_F32BE:H5T_IEEE_F64BE;
   if(H5Tequal(a,le)>0||H5Tequal(a,be)>0){*component=n;valid=1;}
  }
 }
 if(an)H5free_memory(an);if(bn)H5free_memory(bn);if(a>=0)H5Tclose(a);if(b>=0)H5Tclose(b);return valid;
}
static herr_t visit(hid_t root,const char *name,const H5L_info2_t *link,void *arg){
 (void)arg;if(++visited>128){partial=1;return 1;}if(link->type!=H5L_TYPE_HARD){bad=1;return 1;}if(strlen(name)==0||strlen(name)>255){bad=1;return 1;}
 for(const unsigned char *c=(const unsigned char*)name;*c;c++)if(*c<32||*c==127||*c=='/'){bad=1;return 1;}
 H5O_info2_t info;if(H5Oget_info_by_name3(root,name,&info,H5O_INFO_BASIC,H5P_DEFAULT)<0){bad=1;return 1;}
 if(info.type!=H5O_TYPE_DATASET){partial=1;return 0;} if(count>=MAX_ITEMS){partial=1;return 1;}
 hid_t d=H5Dopen2(root,name,H5P_DEFAULT),t=-1,s=-1,p=-1;if(d<0){bad=1;return 1;}
 int ok=0;item *it=&items[count];memset(it,0,sizeof(*it));
 t=H5Dget_type(d);s=H5Dget_space(d);p=H5Dget_create_plist(d);if(t<0||s<0||p<0)goto done;
 if(H5Pget_external_count(p)!=0)goto done;H5D_layout_t layout=H5Pget_layout(p);if(layout!=H5D_COMPACT&&layout!=H5D_CONTIGUOUS&&layout!=H5D_CHUNKED)goto done;
 H5T_class_t klass=H5Tget_class(t);size_t size=H5Tget_size(t),component=0;int complex=0;
 if(klass==H5T_COMPOUND&&matlab){if(!matlab_complex(t,&component))goto done;complex=1;}
 else {
  if(!((klass==H5T_INTEGER&&(size==1||size==2||size==4||size==8))||(klass==H5T_FLOAT&&(size==4||size==8))))goto done;
  if(H5Tget_precision(t)!=size*8||H5Tget_offset(t)!=0)goto done;
  if(klass==H5T_FLOAT){hid_t a=size==4?H5T_IEEE_F32LE:H5T_IEEE_F64LE,b=size==4?H5T_IEEE_F32BE:H5T_IEEE_F64BE;if(H5Tequal(t,a)<=0&&H5Tequal(t,b)<=0)goto done;}
  if(klass==H5T_INTEGER && H5Tget_order(t)!=H5T_ORDER_LE && H5Tget_order(t)!=H5T_ORDER_BE)goto done;
  if(klass==H5T_INTEGER && H5Tget_sign(t)!=H5T_SGN_NONE && H5Tget_sign(t)!=H5T_SGN_2)goto done;
 }
 int rank=H5Sget_simple_extent_ndims(s);if(rank<0||rank>MAX_RANK)goto done;hsize_t dims[4]={1,1,1,1};if(rank&&H5Sget_simple_extent_dims(s,dims,NULL)<0)goto done;
 hsize_t elements=1;for(int i=0;i<rank;i++){if(dims[i]>UINT_MAX||(dims[i]&&elements>UINT_MAX/dims[i]))goto done;elements*=dims[i];it->shape[i]=(unsigned)dims[i];}it->rank=rank;
 hsize_t chunks[4]={0,0,0,0};
 if(layout==H5D_CHUNKED){if(rank==0||H5Pget_chunk(p,rank,chunks)!=rank)goto done;uint64_t n=size;for(int i=0;i<rank;i++){if(!chunks[i]||chunks[i]>8*1024*1024/n)goto done;n*=chunks[i];}}
 int nf=H5Pget_nfilters(p);if(nf<0||nf>3)goto done;for(int i=0;i<nf;i++){unsigned flags=0;size_t n=0;H5Z_filter_t id=H5Pget_filter2(p,i,&flags,&n,NULL,0,NULL,NULL);if(id!=H5Z_FILTER_DEFLATE&&id!=H5Z_FILTER_SHUFFLE&&id!=H5Z_FILTER_FLETCHER32)goto done;}
 if(elements&&H5Dget_storage_size(d)==0)goto done;
 if(matlab){char c[64];if(attr_text(d,"MATLAB_class",c,sizeof(c))!=1)goto done;const char *expected=complex?(component==8?"double":"single"):klass==H5T_FLOAT?(size==8?"double":"single"):NULL;if(expected){if(strcmp(c,expected))goto done;}else {const char *sg=H5Tget_sign(t)==H5T_SGN_NONE?"uint":"int";char tmp[32];snprintf(tmp,sizeof(tmp),"%s%zu",sg,size*8);if(strcmp(c,tmp))goto done;}}
 strncpy(it->name,name,255);it->count=elements<MAX_VALUES?(unsigned)elements:MAX_VALUES;if(elements>MAX_VALUES)partial=1;
 hid_t mem=-1, native=-1;unsigned char data[MAX_VALUES*16];memset(data,0,sizeof(data));
 if(it->count){hsize_t coords[MAX_VALUES*MAX_RANK];for(unsigned i=0;i<it->count;i++){hsize_t n=i;for(int j=rank-1;j>=0;j--){coords[i*rank+j]=n%dims[j];n/=dims[j];}}
 if(layout==H5D_CHUNKED){for(unsigned i=0;i<it->count;i++){hsize_t origin[4],storage=0;for(int j=0;j<rank;j++)origin[j]=(coords[i*rank+j]/chunks[j])*chunks[j];if(H5Dget_chunk_storage_size(d,origin,&storage)<0||storage==0)goto read_done;}}
 if(rank && H5Sselect_elements(s,H5S_SELECT_SET,it->count,coords)<0)goto read_done;hsize_t n=it->count;mem=H5Screate_simple(1,&n,NULL);if(complex){native=H5Tcreate(H5T_COMPOUND,size);hid_t nt=component==8?H5T_NATIVE_DOUBLE:H5T_NATIVE_FLOAT;if(native<0||H5Tinsert(native,"real",0,nt)<0||H5Tinsert(native,"imag",component,nt)<0)goto read_done;}else native=H5Tget_native_type(t,H5T_DIR_DEFAULT);if(mem<0||native<0||H5Tget_size(native)!=size||H5Dread(d,native,mem,s,H5P_DEFAULT,data)<0)goto read_done;
 }
 if(complex){strcpy(it->type,component==8?"complex float64":"complex float32");for(unsigned i=0;i<it->count;i++){double real,imag;if(component==8){memcpy(&real,data+i*size,8);memcpy(&imag,data+i*size+8,8);}else{float r,j;memcpy(&r,data+i*size,4);memcpy(&j,data+i*size+4,4);real=r;imag=j;}snprintf(it->values[i],80,"(%.17g, %.17g)",real,imag);}}
 else if(klass==H5T_FLOAT){strcpy(it->type,size==8?"float64":"float32");for(unsigned i=0;i<it->count;i++){double x;if(size==8)memcpy(&x,data+i*size,8);else{float f;memcpy(&f,data+i*size,4);x=f;}snprintf(it->values[i],40,"%.17g",x);}}
 else{int sign=H5Tget_sign(t)!=H5T_SGN_NONE;snprintf(it->type,sizeof(it->type),"%s%zu",sign?"int":"uint",size*8);for(unsigned i=0;i<it->count;i++){uint64_t x=0;memcpy(&x,data+i*size,size);if(sign&&size<8&&(x&(1ULL<<(size*8-1))))x|=(~0ULL)<<(size*8);if(sign)snprintf(it->values[i],40,"%lld",(long long)(int64_t)x);else snprintf(it->values[i],40,"%llu",(unsigned long long)x);}}
 ok=1;
read_done:if(native>=0)H5Tclose(native);if(mem>=0)H5Sclose(mem);
done:if(p>=0)H5Pclose(p);if(s>=0)H5Sclose(s);if(t>=0)H5Tclose(t);H5Dclose(d);if(ok)count++;else partial=1;return 0;
}
int inspect(int mode){count=0;visited=0;partial=0;bad=0;matlab=mode;H5Eset_auto2(H5E_DEFAULT,NULL,NULL);if(H5PLset_loading_state(0)<0)return 0;hid_t f=H5Fopen("/input",H5F_ACC_RDONLY,H5P_DEFAULT);if(f<0)return 0;if(!mode){char props[256];if(attr_text(f,"_NCProperties",props,sizeof(props))!=1||(strncmp(props,"version=",8)||!strstr(props,",netcdf=")||!strstr(props,",hdf5="))){H5Fclose(f);return 0;}}
 hid_t root=H5Gopen2(f,"/",H5P_DEFAULT);if(root<0){H5Fclose(f);return 0;}hsize_t idx=0;herr_t status=H5Literate2(root,H5_INDEX_NAME,H5_ITER_NATIVE,&idx,visit,NULL);H5Gclose(root);H5Fclose(f);return !bad&&status>=0;}
unsigned item_count(void){return count;}int is_partial(void){return partial;} const char *item_name(unsigned i){return i<count?items[i].name:"";}const char *item_type(unsigned i){return i<count?items[i].type:"";}unsigned item_rank(unsigned i){return i<count?items[i].rank:0;}unsigned item_dim(unsigned i,unsigned j){return i<count&&j<items[i].rank?items[i].shape[j]:0;}unsigned item_length(unsigned i){return i<count?items[i].count:0;}const char *item_value(unsigned i,unsigned j){return i<count&&j<items[i].count?items[i].values[j]:"";}
