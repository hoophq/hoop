(ns webapp.components.combobox
  (:require
   [clojure.string :as string]
   [reagent.core :as r]))

(defn- combobox-icon []
  [:svg {:class "h-5 w-5 text-gray-400"
         :xmlns "http://www.w3.org/2000/svg"
         :viewBox "0 0 20 20"
         :fill "currentColor"
         :aria-hidden "true"}
   [:path {:fill-rule "evenodd"
           :d "M10 3a1 1 0 01.707.293l3 3a1 1 0 01-1.414 1.414L10 5.414 7.707 7.707a1 1 0 01-1.414-1.414l3-3A1 1 0 0110 3zm-3.707 9.293a1 1 0 011.414 0L10 14.586l2.293-2.293a1 1 0 011.414 1.414l-3 3a1 1 0 01-1.414 0l-3-3a1 1 0 010-1.414z"
           :clip-rule "evenodd"}]])

(defn- combobox-list-icon []
  [:svg {:class "h-5 w-5"
         :xmlns "http://www.w3.org/2000/svg"
         :viewBox "0 0 20 20"
         :fill "currentColor"
         :aria-hidden "true"}
   [:path {:fill-rule "evenodd"
           :d "M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
           :clip-rule "evenodd"}]])

(defn- combobox-item [{:keys [item selected on-change close-list]}]
  [:li {:class (str "relative select-none text-gray-900 "
                    "py-2 pl-3 pr-9 text-xs "
                    "hover:bg-gray-100 cursor-pointer "
                    (when (= (:value item) selected) "bg-gray-100 cursor-default "))
        :on-click (fn []
                    (on-change (:value item))
                    (close-list))
        :role "option"
        :tabIndex "-1"}
   [:span {:class "block truncate"}
    (:text item)]
   (when (= (:value item) selected)
     [:span {:class "absolute inset-y-0 right-0 flex items-center pr-4 text-indigo-600"}
      [combobox-list-icon]])])

(defn- search-options [options pattern]
  (filter #(string/includes? (:value %) pattern) options))

(defn main
  "size -> a variation property for a regular sized or a small one. Valid option is :small, if anything else is passed, it will consider the regular
  name -> form name property;
  label -> for adding a label to the combobox. If not provided, the <label> tag won't be rendered;
  options -> a list of values to be rendered. The expected structure is {:value \"\", :text \"\"}, where value is the metadata for the selected information and text is anything you want to show in the option for that value;
  list-classes -> to provide some specific stylezation to the list of options, it is expected to be passed CSS classes;
  placeholder -> placeholder form property for input;
  clear? -> a boolean to set a clear first option in the list
  loading? -> a boolean to manage a loading status for the list
  required? -> a boolean to manage if the input is required or not in the form
  selected -> a string with the selected value (see options);
  on-blur -> a function that will be executed on the input on-blur
  on-focus -> a function that will be executed on the input on-focus
  on-change -> a function triggered whenever the user clicks on another option."
  []
  (let [list-status (r/atom :closed)
        close-list #(reset! list-status :closed)
        open-list #(reset! list-status :open)
        searched-options (r/atom nil)
        input-value (r/atom nil)]
    (fn [{:keys [size name label options list-classes placeholder clear? selected loading? required?
                 on-focus on-blur on-change]}]
      ;; lifecycle-iterable was created to manage first render.
      ;; options is a value that gets always the right value, but search-options is from
      ;; upperscope and needs iteration on events to be available. So, in first render
      ;; and input-value empty we show `options`, otherwise we show search-options
      (let [lifecycle-iterable (if (empty? @searched-options)
                                 options
                                 @searched-options)]
        [:div
         (when label
           [:label {:for name
                    :class "block text-xs font-semibold text-gray-800 mb-x-small"}
            label])
         [:div {:class "relative"}
          [:input {:class (str "w-full rounded-md border shadow-sm h-12 "
                               "border-gray-300 bg-white "
                               "pl-3 pr-12 "
                               (if (= size :small) "py-1 " "py-3 ")
                               "focus:border-indigo-500 focus:outline-none "
                               "focus:ring-1 focus:ring-indigo-500 "
                               "sm:text-sm ")
                   :placeholder placeholder
                   :id name
                   :name name
                   :required (or required? false)
                   :value (or @input-value selected)
                   :autoComplete "off"
                   :on-keyDown (fn [e]
                                 (when (= (.-keyCode e) 27)
                                   (doall
                                    (close-list)
                                    (reset! searched-options nil))))
                   :on-change (fn [e]
                                (let [value (-> e .-target .-value)]
                                  (reset! input-value value)
                                  (reset! searched-options
                                          (search-options options value))))
                 ;; the line below had to be that way because blur event is
                 ;; triggered before the change, so the list stay in there long
                 ;; enough so the clicked item can be captured before it's unmounted
                   :on-blur (fn []
                              (when on-blur (js/setTimeout on-blur 150))
                              (js/setTimeout close-list 150)
                              (js/setTimeout #(reset! searched-options nil) 150))
                   :on-focus (fn []
                               (when on-focus (on-focus))
                               (open-list)
                               (reset! searched-options nil))}]
          [:button {:type "button"
                    :on-click (fn []
                                (open-list)
                                (.focus (. js/document (getElementById name))))
                    :class (str "absolute flex items-center rounded-r-md "
                                "inset-y-0 right-0 px-2 focus:outline-none ")}
           [combobox-icon]]
          (when (= @list-status :open)
            [:ul {:class (str "absolute overflow-auto rounded-md bg-white "
                              "shadow-lg ring-1 ring-black ring-opacity-5 "
                              "z-10 mt-1 max-h-60 w-full py-1 "
                              "text-base focus:outline-none sm:text-sm "
                              (when list-classes list-classes))
                  :id "options"
                  :role "listbox"}
             (when clear?
               [:li {:class (str "relative select-none text-gray-500 "
                                 "py-2 pl-3 pr-9 text-xs "
                                 "hover:bg-gray-100 cursor-pointer "
                                 "border-b")
                     :on-click (fn []
                                 (on-change "")
                                 (reset! input-value "")
                                 (close-list)
                                 (reset! searched-options nil))
                     :role "option"
                     :tabIndex "-1"}
                [:span {:class "block truncate"}
                 "Clear selection"]])
           ;; TODO when searched-options is empty, show a "not found" result
             (when loading?
               [:div {:class "p-small text-xs italic text-gray-500"}
                "loading..."])
             (when (not loading?)
               (for [option lifecycle-iterable]
                 ^{:key option}
                 [combobox-item {:item option
                                 :on-change (fn [value]
                                              (on-change value)
                                              (reset! input-value value))
                                 :close-list close-list
                                 :selected selected}]))])]]))))
