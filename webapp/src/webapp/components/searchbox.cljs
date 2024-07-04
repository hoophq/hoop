(ns webapp.components.searchbox
  (:require
   [clojure.string :as string]
   [reagent.core :as r]
   [webapp.components.icon :as icon]))

(defn- searchbox-icon []
  [:svg {:class "h-5 w-5 text-gray-400"
         :xmlns "http://www.w3.org/2000/svg"
         :viewBox "0 0 20 20"
         :fill "none"
         :aria-hidden "true"}
   [:circle {:cx "8.5"
             :cy "8.5"
             :r "5.75"
             :stroke "currentColor"
             :stroke-width "1.5"
             :stroke-linecap "round"
             :stroke-linejoin "round"}]
   [:path {:d "M17.25 17.25L13 13"
           :stroke "currentColor"
           :stroke-width "1.5"
           :stroke-linecap "round"
           :stroke-linejoin "round"}]])

(defn- no-results []
  [:div {:class "flex gap-regular items-center"}
   [:figure
    {:class "flex-shrink w-32 mx-auto p-regular"}
    [:img {:src "/images/illustrations/pc+monitor.svg"
           :class "w-full"}]]
   [:div {:class "flex-grow"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "No results with this criteria."]
    [:div {:class "text-gray-300 text-xs pb-x-small"}
     "Maybe some typo?"]
    [:div {:class "text-gray-500 text-xs"}
     "We do not consider spaces, traces (-) or underscores (_)."]]])

(defn- searchbox-list-icon []
  [:svg {:class "h-5 w-5"
         :xmlns "http://www.w3.org/2000/svg"
         :viewBox "0 0 20 20"
         :fill "currentColor"
         :aria-hidden "true"}
   [:path {:fill-rule "evenodd"
           :d "M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
           :clip-rule "evenodd"}]])

(defn- searchbox-item [{:keys [item display-key meta-display-keys selected on-change close-list searchable-keys]}]
  (let [meta-values-string (string/join " " (map #(str (% item)) meta-display-keys))]
    [:li {:class (str "relative select-none text-gray-900 "
                      "py-2 pl-3 pr-9 text-xs "
                      "hover:bg-gray-100 cursor-pointer "
                      (when (= (:value item) selected) "bg-gray-100 cursor-default "))
          :on-click (fn []
                      (on-change item)
                      (close-list))
          :role "option"
          :tabIndex "-1"}
     [:span {:class "block truncate"}
      (display-key item)
      (when meta-display-keys
        [:span {:class "text-gray-500 italic"}
         (str " " meta-values-string)])]
     [:div {:class "gap-small text-xxs"}
      [:span {:class "font-bold text-gray-500 pr-x-small"} "{"]
      (for [[item-key item-value] item]
        (when (some #(= item-key %) searchable-keys)
          [:span {:key (str item-key item-value)
                  :class "flex-shrink gap-x-small whitespace-normal"}
           [:span {:class "text-gray-500 pr-x-small"} item-key]
           [:span {:class "font-bold text-gray-500 pr-x-small"} item-value]]))
      [:span {:class "font-bold text-gray-500"} "}"]]
     (when (= (:value item) selected)
       [:span {:class "absolute inset-y-0 right-0 flex items-center pr-4 text-indigo-600"}
        [searchbox-list-icon]])]))

(def input-style
  (str "w-full rounded-md border shadow-sm "
       "border-gray-300 bg-white "
       "pl-3 pr-12 "
       "focus:border-indigo-500 focus:outline-none "
       "focus:ring-1 focus:ring-indigo-500 "
       "sm:text-sm"))

(def input-style-dark
  (str "w-full rounded-md border shadow-sm "
       "border-gray-400 bg-transparent text-white "
       "pl-3 pr-12 "
       "focus:border-white focus:outline-none "
       "focus:ring-1 focus:ring-white "
       "sm:text-sm"))

(defn- search-options [options pattern searchable-keys]
  (filter #(string/includes?
            (string/replace (string/join " " (vals
                                              (select-keys % searchable-keys)))
                            #"-|_" "")
            (string/replace pattern #" |-|_" "")) options))

(defn main
  " SEARCHBOX component searches for an item with by a set of values from a shallow object.
  EXAMPLE: given the map {:name :john :last-name :doe :nationality :brazilian}, every value (pointed by searchable-keys) is searchable and will point to its choosen key
  size -> a variation property for a regular sized or a small one. Valid option is :small, if anything else is passed, it will consider the regular
  name -> form name property;
  label -> for adding a label to the combobox. If not provided, the <label> tag won't be rendered;
  list-classes -> to provide some specific stylezation to the list of options, it is expected to be passed CSS classes;
  placeholder -> placeholder form property for input;
  clear? -> a boolean to set a clear first option in the list
  loading? -> a boolean for managing a loading status in the search box list
  selected -> a string with the selected value (see options);
  on-select-result -> a function triggered whenever the user clicks on an result.
  on-change-results-cb -> a callback function to be used on upperscope to have access to the results and manage anything that might be of the upperscope interest
  on-change -> a callback that sends the value in the input for every change in it
  hide-results-list -> a boolean used to do not show the results list. Usually useful with `on-change-results-cb` and the list is not necessary because the results are shown in the upperscope
  on-focus -> a function that will be executed on input focus
  on-blur -> a function that will be executed on input blur
  options -> a list of hashmaps to be rendered searched. Example [{:name \"name\" :type \"type\" :review_type \"review_type\" :redact \"redact\"}]
  display-key -> the key that will be used to display information in an user friendly way. This key must be from a valid key from options. Example :name
  meta-display-keys -> meta information keys from a option that you want to put to the side of display-key. Example: [:name :type]
  searchable-keys -> the keys from the options that you want to be searchable. Example: [:name :type :review_type :redact]
  "
  [{:keys [options]}]

  (let [list-status (r/atom :closed)
        close-list #(reset! list-status :closed)
        open-list #(reset! list-status :open)
        searched-options (r/atom options)
        input-value (r/atom nil)]
    (fn [{:keys [size
                 dark
                 name
                 label
                 list-classes
                 placeholder
                 clear?
                 loading?
                 selected
                 on-select-result
                 on-change
                 on-change-results-cb
                 hide-results-list
                 on-focus
                 on-blur
                 options
                 display-key
                 meta-display-keys
                 searchable-keys]}]
      ;; lifecycle-iterable was created to manage first render.
      ;; options is a value that gets always the right value, but search-options is from
      ;; upperscope and needs iteration on events to be available. So, in first render
      ;; and input-value empty we show `options`, otherwise we show search-options
      (let [lifecycle-iterable (if (empty? @searched-options)
                                 options
                                 @searched-options)
            no-results? (and (empty? @searched-options)
                             (> (count @input-value) 0))]
        [:div
         (when label
           [:label {:for name
                    :class "block text-xs font-semibold text-gray-800 mb-x-small"}
            label])
         [:div {:class "relative"}
          [:input {:class (str (if dark
                                 input-style-dark
                                 input-style)
                               (if (= size :small)
                                 "py-1 h-8 "
                                 "py-3 h-12 "))
                   :placeholder placeholder
                   :id name
                   :name name
                   :value (or @input-value selected)
                   :autoComplete "off"
                   :on-keyDown (fn [e]
                                 (when (= (.-keyCode e) 27)
                                   (close-list)))
                   :on-change (fn [e]
                                (let [value (-> e .-target .-value)
                                      results (search-options options
                                                              value
                                                              searchable-keys)]
                                  (reset! input-value value)
                                  (when on-change (on-change value))
                                  (when (not loading?)
                                    (reset! searched-options results)
                                    (when on-change-results-cb
                                      (on-change-results-cb results)))))
                 ;; the line below had to be that way because blur event is
                 ;; triggered before the change, so the list stay in there long
                 ;; enough so the clicked item can be captured before it's unmounted
                   :on-blur (fn []
                              (when on-blur (on-blur))
                              (js/setTimeout close-list 150))
                   :on-focus (fn []
                               (when on-focus (on-focus))
                               (open-list)
                               (reset! searched-options options))}]
          (when loading?
            [:div {:class "absolute w-4 h-4 inset-y-4 right-10 opacity-50 animate-spin origin-center"}
             [icon/regular {:size 4
                            :icon-name "loader-circle"}]])
          [:button {:type "button"
                    :on-click (fn []
                                (open-list)
                                (.focus (. js/document (getElementById name))))
                    :class (str "absolute flex items-center rounded-r-md "
                                "inset-y-0 right-0 px-2 focus:outline-none ")}
           [searchbox-icon]]
          (when (and
                 (= @list-status :open)
                 (not hide-results-list))
            [:ul {:class (str "absolute overflow-auto rounded-md bg-white "
                              "shadow-lg ring-1 ring-black ring-opacity-5 "
                              "z-10 mt-1 max-h-80 w-full py-1 "
                              "text-base focus:outline-none sm:text-sm "
                              (when list-classes list-classes))
                  :id "options"
                  :role "listbox"}
             (when clear?
               [:li {:class (str "relative select-none text-gray-500 "
                                 "py-1 pl-3 pr-9 text-xs "
                                 "hover:bg-gray-100 cursor-pointer "
                                 "border-b")
                     :on-click (fn []
                                 (on-select-result "")
                                 (reset! input-value "")
                                 (close-list))
                     :role "option"
                     :tabIndex "-1"}
                [:span {:class "block truncate"}
                 "Clear"]])
             (when no-results?
               [no-results])
             (when loading?
               [:div {:class "text-xs text-gray-500 italic p-small"}
                "loading..."])
             (when (and (not loading?)
                        (not no-results?))
               (for [option lifecycle-iterable]
                 ^{:key (:name option)}
                 [searchbox-item {:item option
                                  :meta-display-keys meta-display-keys
                                  :display-key display-key
                                  :searchable-keys searchable-keys
                                  :on-change (fn [value]
                                               (reset! input-value (display-key value))
                                               (on-select-result value))
                                  :close-list close-list
                                  :selected selected}]))])]]))))
