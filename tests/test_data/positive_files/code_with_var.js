// JavaScript with var declarations
function oldStyleFunction() {
  var userName = 'john';
  var userAge = 25;
  
  if (userAge > 18) {
    var canVote = true;
    var status = 'adult';
  }
  
  for (var i = 0; i < 10; i++) {
    var item = 'item_' + i;
    console.log(item);
  }
  
  var result = {
    name: userName,
    age: userAge,
    voting: canVote
  };
  
  return result;
}